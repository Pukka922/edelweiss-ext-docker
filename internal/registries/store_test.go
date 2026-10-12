package registries

import (
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/pkg/authconfig"
)

type chownCall struct {
	path     string
	uid, gid int
}

// testStore is a store in a temporary directory whose group file names docker with GID 2375; Chown only records.
func testStore(t *testing.T) (*Store, *[]chownCall) {
	t.Helper()
	dir := t.TempDir()
	groups := filepath.Join(dir, "group")
	if err := os.WriteFile(groups, []byte("root:x:0:root\nwheel:x:10:anna\ndocker:x:2375:anna\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var calls []chownCall
	s := NewStore(filepath.Join(dir, "state", "registries.db"))
	s.GroupFile = groups
	s.Chown = func(path string, uid, gid int) error {
		calls = append(calls, chownCall{filepath.Base(path), uid, gid})
		return nil
	}
	return s, &calls
}

// inspect opens the database of s read-only, as a member of the group docker does.
func inspect(t *testing.T, s *Store) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+s.Path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestEmptyWithoutFile(t *testing.T) {
	s, _ := testStore(t)
	list, err := s.List()
	if err != nil || list == nil || len(list) != 0 {
		t.Fatalf("List = %v, %v; want an empty list", list, err)
	}
}

func TestAddUpdateDelete(t *testing.T) {
	s, calls := testStore(t)
	hub, err := s.Add(Input{Name: "Docker Hub", Address: "https://index.docker.io/v1/", Auth: true, Username: "pukka", Password: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if len(hub.ID) != 16 || hub.Address != DockerHub || hub.Password != "s3cret" {
		t.Fatalf("added %+v", hub)
	}
	local, err := s.Add(Input{Name: "LAN", Address: "pi.local:5000"})
	if err != nil {
		t.Fatal(err)
	}
	if local.Username != "" || local.Password != "" {
		t.Fatalf("an entry without login keeps no credentials: %+v", local)
	}
	if list, _ := s.List(); len(list) != 2 || list[0].ID != hub.ID || list[1].ID != local.ID {
		t.Fatalf("the list is not in the order of adding: %+v", list)
	}

	// an empty password keeps the stored one
	hub, err = s.Update(hub.ID, Input{Name: "Hub", Address: "docker.io", Auth: true, Username: "pukka2"})
	if err != nil || hub.Password != "s3cret" || hub.Username != "pukka2" || hub.Name != "Hub" {
		t.Fatalf("update: %+v, %v", hub, err)
	}
	// "without login" drops the credentials
	hub, err = s.Update(hub.ID, Input{Name: "Hub", Address: "docker.io"})
	if err != nil || hub.Username != "" || hub.Password != "" {
		t.Fatalf("update without login: %+v, %v", hub, err)
	}

	if err := s.Delete(local.ID); err != nil {
		t.Fatal(err)
	}
	list, _ := s.List()
	if len(list) != 1 || list[0].ID != hub.ID {
		t.Fatalf("list after delete: %+v", list)
	}

	// the file: one row, schema version 1, no journal left, root:docker 0640 in a 0750 directory
	db := inspect(t, s)
	var version, rows int
	if err := db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != len(schema) {
		t.Fatalf("user_version %d, %v", version, err)
	}
	if err := db.QueryRow("SELECT count(*) FROM registries").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("%d rows, %v", rows, err)
	}
	if st, _ := os.Stat(s.Path); st.Mode().Perm() != 0o640 {
		t.Fatalf("file mode %v, want 0640", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Dir(s.Path)); st.Mode().Perm() != 0o750 {
		t.Fatalf("directory mode %v, want 0750", st.Mode().Perm())
	}
	if _, err := os.Stat(s.Path + "-journal"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a journal was left behind: %v", err)
	}
	for _, c := range *calls {
		if c.uid != 0 || c.gid != 2375 {
			t.Fatalf("chown %+v, want root:2375", c)
		}
	}
	if len(*calls) == 0 {
		t.Fatal("no chown")
	}
}

func TestValidation(t *testing.T) {
	s, _ := testStore(t)
	if _, err := s.Add(Input{Name: "A", Address: "ghcr.io"}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		in    Input
		field string
	}{
		{Input{Name: "", Address: "x.io"}, "name"},
		{Input{Name: string(make([]rune, 65)), Address: "x.io"}, "name"},
		{Input{Name: "B", Address: "bad host"}, "address"},
		{Input{Name: "B", Address: "y.io", Auth: true, Password: "p"}, "username"},
		{Input{Name: "B", Address: "y.io", Auth: true, Username: "u"}, "password"},
	}
	for _, tc := range cases {
		var fe *FieldError
		if _, err := s.Add(tc.in); !errors.As(err, &fe) || fe.Field != tc.field {
			t.Errorf("Add(%+v) = %v, want a FieldError for %s", tc.in, err, tc.field)
		}
	}
	if _, err := s.Add(Input{Name: "Again", Address: "https://GHCR.io/"}); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate address: %v", err)
	}
	if _, err := s.Update("0000000000000000", Input{Name: "x", Address: "z.io"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	if err := s.Delete("0000000000000000"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete of an unknown id: %v", err)
	}
}

func TestCorruptFileIsNotOverwritten(t *testing.T) {
	s, _ := testStore(t)
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o750); err != nil {
		t.Fatal(err)
	}
	garbage := strings.Repeat("not a database ", 64)
	if err := os.WriteFile(s.Path, []byte(garbage), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("List: %v", err)
	}
	if _, err := s.Add(Input{Name: "A", Address: "ghcr.io"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Add: %v", err)
	}
	if data, _ := os.ReadFile(s.Path); string(data) != garbage {
		t.Fatal("the corrupt file was overwritten")
	}
}

func TestNewerSchemaIsNotOverwritten(t *testing.T) {
	s, _ := testStore(t)
	if _, err := s.Add(Input{Name: "A", Address: "ghcr.io"}); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("PRAGMA user_version = 9"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := s.List(); !errors.Is(err, ErrCorrupt) || !strings.Contains(err.Error(), "newer version") {
		t.Fatalf("List: %v", err)
	}
	if _, err := s.Add(Input{Name: "B", Address: "quay.io"}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Add: %v", err)
	}
	var version int
	if err := inspect(t, s).QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 9 {
		t.Fatalf("user_version %d, %v", version, err)
	}
}

func TestReadOnlyAccess(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may write anything")
	}
	s, _ := testStore(t)
	if _, err := s.Add(Input{Name: "GHCR", Address: "ghcr.io", Auth: true, Username: "octo", Password: "gh-pw"}); err != nil {
		t.Fatal(err)
	}
	// as a member of the group docker sees it: read permission only, on file and directory
	dir := filepath.Dir(s.Path)
	if err := os.Chmod(s.Path, 0o440); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o550); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o750) })
	if list, err := s.List(); err != nil || len(list) != 1 {
		t.Fatalf("List: %+v, %v", list, err)
	}
	if enc, err := s.AuthFor("ghcr.io/octo/app:1"); err != nil || enc == "" {
		t.Fatalf("AuthFor: %q, %v", enc, err)
	}
}

func TestUnreadableFile(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}
	s, _ := testStore(t)
	if _, err := s.Add(Input{Name: "A", Address: "ghcr.io"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(s.Path, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(); !errors.Is(err, ErrForbidden) {
		t.Fatalf("List: %v", err)
	}
}

func TestAuthFor(t *testing.T) {
	s, _ := testStore(t)
	if _, err := s.Add(Input{Name: "Hub", Address: "docker.io", Auth: true, Username: "pukka", Password: "hub-pw"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(Input{Name: "LAN", Address: "localhost:5000"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(Input{Name: "GHCR", Address: "ghcr.io", Auth: true, Username: "octo", Password: "gh-pw"}); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ user, pass, server string }{
		"nginx:1.29":             {"pukka", "hub-pw", "https://index.docker.io/v1/"},
		"ghcr.io/octo/app:1":     {"octo", "gh-pw", "ghcr.io"},
		"localhost:5000/a/b:1":   {}, // an entry without login: anonymous
		"quay.io/coreos/etcd:v3": {}, // no entry
	}
	for ref, want := range cases {
		enc, err := s.AuthFor(ref)
		if err != nil {
			t.Fatal(err)
		}
		if want.user == "" {
			if enc != "" {
				t.Errorf("%s: want an anonymous pull, got %q", ref, enc)
			}
			continue
		}
		cfg, err := authconfig.Decode(enc)
		if err != nil || cfg.Username != want.user || cfg.Password != want.pass || cfg.ServerAddress != want.server {
			t.Errorf("%s: %+v, %v", ref, cfg, err)
		}
	}
}
