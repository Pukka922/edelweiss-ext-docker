package registries

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

var (
	anna  = sdk.User{Name: "anna", UID: 1000}
	admin = sdk.User{Name: "anna", UID: 1000, Admin: true}
)

func testApp(s *Store, d dockerapi.Docker) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, s, d)
	return app
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

type listAnswer struct {
	Registries []View `json:"registries"`
	Writable   bool   `json:"writable"`
}

func TestRoutes(t *testing.T) {
	s, _ := testStore(t)
	app := testApp(s, &dockertest.Fake{})
	body := `{"name":"GHCR","address":"ghcr.io","auth":true,"username":"octo","password":"gh-pw"}`

	if resp := sdktest.Do(t, app, sdktest.Request("POST", "/registries", strings.NewReader(body), anna)); resp.StatusCode != 403 {
		t.Fatalf("add without admin mode: %d", resp.StatusCode)
	}
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/registries", strings.NewReader(body), admin))
	if resp.StatusCode != 201 {
		t.Fatalf("add: %d", resp.StatusCode)
	}
	added := sdktest.DecodeJSON[View](t, resp)
	if added.Address != "ghcr.io" || added.Username != "octo" || !added.HasPassword {
		t.Fatalf("added %+v", added)
	}

	list := sdktest.DecodeJSON[listAnswer](t, sdktest.Do(t, app, sdktest.Request("GET", "/registries", nil, anna)))
	if len(list.Registries) != 1 || list.Writable {
		t.Fatalf("list as user: %+v", list)
	}
	raw := readBody(t, sdktest.Do(t, app, sdktest.Request("GET", "/registries", nil, admin)))
	if strings.Contains(raw, "gh-pw") || !strings.Contains(raw, `"writable":true`) {
		t.Fatalf("list as admin leaks the password or is not writable: %s", raw)
	}

	upd := `{"name":"GitHub","address":"ghcr.io","auth":true,"username":"octo","password":""}`
	resp = sdktest.Do(t, app, sdktest.Request("PUT", "/registries/"+added.ID, strings.NewReader(upd), admin))
	if got := sdktest.DecodeJSON[View](t, resp); resp.StatusCode != 200 || got.Name != "GitHub" || !got.HasPassword {
		t.Fatalf("update: %d %+v", resp.StatusCode, got)
	}

	bad := sdktest.Do(t, app, sdktest.Request("POST", "/registries", strings.NewReader(`{"name":"","address":"x.io"}`), admin))
	if bad.StatusCode != 400 || !strings.Contains(readBody(t, bad), `"field":"name"`) {
		t.Fatalf("invalid input: %d", bad.StatusCode)
	}
	dup := sdktest.Do(t, app, sdktest.Request("POST", "/registries", strings.NewReader(body), admin))
	if dup.StatusCode != 409 {
		t.Fatalf("duplicate: %d", dup.StatusCode)
	}

	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/registries/"+added.ID, nil, admin)); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/registries/"+added.ID, nil, admin)); resp.StatusCode != 404 {
		t.Fatalf("delete again: %d", resp.StatusCode)
	}
}

func TestLoginTest(t *testing.T) {
	s, _ := testStore(t)
	stored, err := s.Add(Input{Name: "GHCR", Address: "ghcr.io", Auth: true, Username: "octo", Password: "gh-pw"})
	if err != nil {
		t.Fatal(err)
	}
	var got []client.RegistryLoginOptions
	answer := error(nil)
	f := &dockertest.Fake{RegistryLoginFn: func(_ context.Context, o client.RegistryLoginOptions) (client.RegistryLoginResult, error) {
		got = append(got, o)
		return client.RegistryLoginResult{}, answer
	}}
	app := testApp(s, f)

	// a stored entry: allowed without admin mode
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/registries/test", strings.NewReader(`{"id":"`+stored.ID+`"}`), anna))
	if r := sdktest.DecodeJSON[TestResult](t, resp); !r.OK || got[0].Username != "octo" || got[0].Password != "gh-pw" || got[0].ServerAddress != "ghcr.io" {
		t.Fatalf("stored: %+v %+v", r, got)
	}
	// form values: admin mode only; with id, an empty password uses the stored one
	form := `{"id":"` + stored.ID + `","address":"ghcr.io","auth":true,"username":"octo","password":""}`
	if resp := sdktest.Do(t, app, sdktest.Request("POST", "/registries/test", strings.NewReader(form), anna)); resp.StatusCode != 403 {
		t.Fatalf("form test without admin mode: %d", resp.StatusCode)
	}
	sdktest.Do(t, app, sdktest.Request("POST", "/registries/test", strings.NewReader(form), admin))
	if got[1].Password != "gh-pw" {
		t.Fatalf("form test with stored password: %+v", got[1])
	}
	// Docker Hub logins go to Docker's historical server address
	sdktest.Do(t, app, sdktest.Request("POST", "/registries/test", strings.NewReader(`{"address":"docker.io"}`), admin))
	if got[2].ServerAddress != "https://index.docker.io/v1/" || got[2].Username != "" {
		t.Fatalf("hub without login: %+v", got[2])
	}

	// a refused login is a result, not an error
	answer = errors.New("Error response from daemon: login attempt to https://ghcr.io/v2/ failed with status: 401 Unauthorized")
	resp = sdktest.Do(t, app, sdktest.Request("POST", "/registries/test", strings.NewReader(`{"id":"`+stored.ID+`"}`), anna))
	if r := sdktest.DecodeJSON[TestResult](t, resp); resp.StatusCode != 200 || r.OK || !strings.HasPrefix(r.Message, "login attempt") {
		t.Fatalf("refused: %d %+v", resp.StatusCode, r)
	}
	// no Docker is an error
	answer = dockertest.Unavailable(t)
	resp = sdktest.Do(t, app, sdktest.Request("POST", "/registries/test", strings.NewReader(`{"id":"`+stored.ID+`"}`), anna))
	if resp.StatusCode != 503 {
		t.Fatalf("unavailable: %d", resp.StatusCode)
	}
}

func TestListForbidden(t *testing.T) {
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
	resp := sdktest.Do(t, testApp(s, &dockertest.Fake{}), sdktest.Request("GET", "/registries", nil, anna))
	if resp.StatusCode != 403 || !strings.Contains(readBody(t, resp), "registries_forbidden") {
		t.Fatalf("status %d", resp.StatusCode)
	}
}
