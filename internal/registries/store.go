// Package registries keeps the extension's list of container registries with their logins in a SQLite database, and
// answers which login a pull of an image reference uses.
package registries

import (
	"bufio"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/registry"
	_ "modernc.org/sqlite" // the database/sql driver "sqlite" (pure Go)
)

// DefaultPath is where the list lives on the host: in the extension's own directory, which edelweiss deletes when the
// extension is removed.
const DefaultPath = "/var/lib/edelweiss-ext-docker/registries.db"

// schema are the steps of the database in the order they are applied; PRAGMA user_version counts the applied ones.
// Only ever append steps.
var schema = []string{
	`CREATE TABLE registries (
		id       TEXT PRIMARY KEY,
		name     TEXT NOT NULL,
		address  TEXT NOT NULL UNIQUE,
		username TEXT NOT NULL DEFAULT '',
		password TEXT NOT NULL DEFAULT ''
	)`,
}

// Registry is one entry of the list.
type Registry struct {
	ID       string
	Name     string
	Address  string
	Username string
	Password string
}

// Input is an entry as the UI sends it.
type Input struct {
	Name    string `json:"name"`
	Address string `json:"address"`
	// Auth false means "without login": username and password are dropped.
	Auth     bool   `json:"auth"`
	Username string `json:"username"`
	// Password may stay empty when an entry with a login is changed: the stored one is kept.
	Password string `json:"password"`
}

var (
	ErrForbidden = errors.New("the registry list is not readable for this user")
	ErrCorrupt   = errors.New("the registry list cannot be read")
	ErrNotFound  = errors.New("no such registry")
	ErrExists    = errors.New("a registry with this address exists already")
)

// FieldError is an invalid field of an Input.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + " " + e.Message }

// Store reads and writes the list. Changing it needs root, i.e. the backend in admin mode; members of the group docker
// read it.
type Store struct {
	Path string
	// GroupFile is where the GID of the group docker is looked up; that group owns the database, so its members can use
	// the logins for pulls (/etc/group; tests use a fixture).
	GroupFile string
	// Chown sets the owner of a path (os.Chown; tests, which do not run as root, record the calls).
	Chown func(path string, uid, gid int) error

	mu sync.Mutex
}

// NewStore returns the store of the database at path.
func NewStore(path string) *Store {
	return &Store{Path: path, GroupFile: "/etc/group", Chown: os.Chown}
}

// List returns the registries in the order they were added; without a database the list is empty.
func (s *Store) List() ([]Registry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.read()
}

// Find returns the entry id.
func (s *Store) Find(id string) (Registry, error) {
	list, err := s.List()
	if err != nil {
		return Registry{}, err
	}
	for _, r := range list {
		if r.ID == id {
			return r, nil
		}
	}
	return Registry{}, fmt.Errorf("%w: %s", ErrNotFound, id)
}

// Add appends an entry.
func (s *Store) Add(in Input) (Registry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Registry
	err := s.write(func(ctx context.Context, tx *sql.Tx, list []Registry) error {
		var err error
		if r, err = build(in, Registry{}, list); err != nil {
			return err
		}
		if r.ID, err = newID(); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO registries (id, name, address, username, password) VALUES (?, ?, ?, ?, ?)`,
			r.ID, r.Name, r.Address, r.Username, r.Password)
		return err
	})
	return r, err
}

// Update changes the entry id.
func (s *Store) Update(id string, in Input) (Registry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var r Registry
	err := s.write(func(ctx context.Context, tx *sql.Tx, list []Registry) error {
		for _, old := range list {
			if old.ID != id {
				continue
			}
			var err error
			if r, err = build(in, old, list); err != nil {
				return err
			}
			r.ID = id
			_, err = tx.ExecContext(ctx, `UPDATE registries SET name = ?, address = ?, username = ?, password = ? WHERE id = ?`,
				r.Name, r.Address, r.Username, r.Password, id)
			return err
		}
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	})
	return r, err
}

// Delete removes the entry id.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.write(func(ctx context.Context, tx *sql.Tx, _ []Registry) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM registries WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err == nil && n == 0 {
			return fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return err
	})
}

// AuthFor returns the X-Registry-Auth value for a pull of ref: the login of the entry for its registry, or "" for an
// anonymous pull.
func (s *Store) AuthFor(ref string) (string, error) {
	list, err := s.List()
	if err != nil {
		return "", err
	}
	host := HostOf(ref)
	for _, r := range list {
		if r.Address == host && r.Username != "" {
			return authconfig.Encode(registry.AuthConfig{Username: r.Username, Password: r.Password, ServerAddress: ServerAddress(r.Address)})
		}
	}
	return "", nil
}

// read opens the database read-only, as every member of the group docker can, and returns the list.
func (s *Store) read() ([]Registry, error) {
	if err := s.readable(); errors.Is(err, fs.ErrNotExist) {
		return []Registry{}, nil
	} else if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(s.Path, false))
	if err != nil {
		return nil, s.corrupt(err)
	}
	defer db.Close()
	return s.load(context.Background(), db)
}

// readable checks the file before SQLite opens it, because SQLite reports a missing permission only as "unable to open
// database file".
func (s *Store) readable() error {
	f, err := os.Open(s.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return err
	case errors.Is(err, fs.ErrPermission):
		return ErrForbidden
	case err != nil:
		return s.corrupt(err)
	}
	return f.Close()
}

type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// load reads the list through q (the database or a transaction on it).
func (s *Store) load(ctx context.Context, q querier) ([]Registry, error) {
	version, err := s.version(ctx, q)
	if err != nil {
		return nil, err
	}
	if version == 0 {
		return []Registry{}, nil // created, never written
	}
	rows, err := q.QueryContext(ctx, `SELECT id, name, address, username, password FROM registries ORDER BY rowid`)
	if err != nil {
		return nil, s.corrupt(err)
	}
	defer rows.Close()
	list := []Registry{}
	for rows.Next() {
		var r Registry
		if err := rows.Scan(&r.ID, &r.Name, &r.Address, &r.Username, &r.Password); err != nil {
			return nil, s.corrupt(err)
		}
		list = append(list, r)
	}
	if err := rows.Err(); err != nil {
		return nil, s.corrupt(err)
	}
	return list, nil
}

// version reads the schema version of the database; one of a newer version of the extension is refused.
func (s *Store) version(ctx context.Context, q querier) (int, error) {
	var v int
	if err := q.QueryRowContext(ctx, "PRAGMA user_version").Scan(&v); err != nil {
		return 0, s.corrupt(err)
	}
	if v > len(schema) {
		return 0, fmt.Errorf("%w: %s was written by a newer version of the extension (schema %d)", ErrCorrupt, s.Path, v)
	}
	return v, nil
}

func (s *Store) corrupt(err error) error { return fmt.Errorf("%w: %s: %v", ErrCorrupt, s.Path, err) }

// write runs change in one transaction, after bringing the schema of the database up to date, and then sets the rights
// of directory and database: root and the group docker, 0750 and 0640. A file that is no database of this extension,
// or one of a newer version, is never changed: reading its schema version fails before anything is written.
func (s *Store) write(change func(ctx context.Context, tx *sql.Tx, list []Registry) error) error {
	ctx := context.Background()
	gid := dockerGID(s.GroupFile)
	dir := filepath.Dir(s.Path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o750); err != nil {
		return err
	}
	if err := s.Chown(dir, 0, gid); err != nil {
		return err
	}
	db, err := sql.Open("sqlite", dsn(s.Path, true))
	if err != nil {
		return s.corrupt(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return s.corrupt(err)
	}
	defer tx.Rollback()
	version, err := s.version(ctx, tx)
	if err != nil {
		return err
	}
	for _, step := range schema[version:] {
		if _, err := tx.ExecContext(ctx, step); err != nil {
			return err
		}
	}
	if version < len(schema) {
		if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", len(schema))); err != nil {
			return err
		}
	}
	list, err := s.load(ctx, tx)
	if err != nil {
		return err
	}
	if err := change(ctx, tx, list); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if err := os.Chmod(s.Path, 0o640); err != nil {
		return err
	}
	return s.Chown(s.Path, 0, gid)
}

// dsn is the connection string of the database: read-only for readers (mode=ro – it needs the file: form, a plain path
// would ignore it); for the writer the rollback journal (DELETE) and BEGIN IMMEDIATE. WAL is out: readers without write
// access to the directory could not create its -shm file.
func dsn(path string, write bool) string {
	q := "mode=ro&_pragma=busy_timeout(5000)"
	if write {
		q = "mode=rwc&_pragma=busy_timeout(5000)&_pragma=journal_mode(DELETE)&_txlock=immediate"
	}
	return (&url.URL{Scheme: "file", Path: path, RawQuery: q}).String()
}

// build checks in against the other entries of list. old is the stored entry when one is changed: its password stays
// when in has none.
func build(in Input, old Registry, list []Registry) (Registry, error) {
	name := strings.TrimSpace(in.Name)
	if name == "" || utf8.RuneCountInString(name) > 64 {
		return Registry{}, &FieldError{"name", "must have 1 to 64 characters"}
	}
	addr, err := NormalizeAddress(in.Address)
	if err != nil {
		return Registry{}, &FieldError{"address", err.Error()}
	}
	for _, r := range list {
		if r.Address == addr && r.ID != old.ID {
			return Registry{}, fmt.Errorf("%w: %s", ErrExists, addr)
		}
	}
	r := Registry{Name: name, Address: addr}
	if in.Auth {
		r.Username = strings.TrimSpace(in.Username)
		if r.Username == "" {
			return Registry{}, &FieldError{"username", "is required for a login"}
		}
		r.Password = in.Password
		if r.Password == "" {
			r.Password = old.Password
		}
		if r.Password == "" {
			return Registry{}, &FieldError{"password", "is required for a login"}
		}
	}
	return r, nil
}

// dockerGID is the GID of the group docker in groupFile, or 0 (root) without one.
func dockerGID(groupFile string) int {
	f, err := os.Open(groupFile)
	if err != nil {
		return 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Split(sc.Text(), ":")
		if len(fields) >= 3 && fields[0] == "docker" {
			if gid, err := strconv.Atoi(fields[2]); err == nil {
				return gid
			}
		}
	}
	return 0
}

func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
