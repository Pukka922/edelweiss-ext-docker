package dockerapi_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
)

func daemonError(msg string, class error) error {
	return fmt.Errorf("Error response from daemon: %s: %w", msg, class)
}

func TestError(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
		msg    string
	}{
		{"unavailable", dockertest.Unavailable(t), 503, "docker_unavailable", ""},
		{"not found", daemonError("No such container: web", cerrdefs.ErrNotFound), 404, "not_found", "No such container: web"},
		{"conflict", daemonError("name in use", cerrdefs.ErrConflict), 409, "conflict", "name in use"},
		{"invalid", daemonError("bad port", cerrdefs.ErrInvalidArgument), 400, "invalid_request", "bad port"},
		{"other", errors.New("Error response from daemon: boom"), 502, "docker_error", "boom"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var apiErr *sdk.APIError
			if !errors.As(dockerapi.Error(tc.err), &apiErr) {
				t.Fatalf("not an APIError: %v", dockerapi.Error(tc.err))
			}
			if apiErr.Status != tc.status || apiErr.Code != tc.code {
				t.Fatalf("%d %s, want %d %s", apiErr.Status, apiErr.Code, tc.status, tc.code)
			}
			if tc.msg != "" && !strings.HasPrefix(apiErr.Message, tc.msg) {
				t.Fatalf("message %q, want prefix %q", apiErr.Message, tc.msg)
			}
		})
	}
	if dockerapi.Error(nil) != nil {
		t.Fatal("Error(nil) must be nil")
	}
	own := sdk.NewError(409, "image_in_use", "x")
	if dockerapi.Error(own) != own {
		t.Fatal("an APIError must pass through unchanged")
	}
}

func TestForbidden(t *testing.T) {
	err := dockertest.Forbidden(t) // skips as root
	if got := dockerapi.AccessOf(err); got != dockerapi.AccessForbidden {
		t.Fatalf("AccessOf = %q", got)
	}
	var apiErr *sdk.APIError
	if !errors.As(dockerapi.Error(err), &apiErr) || apiErr.Status != 403 || apiErr.Code != "docker_forbidden" {
		t.Fatalf("Error = %v", dockerapi.Error(err))
	}
}

func TestAccessOf(t *testing.T) {
	if got := dockerapi.AccessOf(nil); got != dockerapi.AccessOK {
		t.Fatalf("nil: %q", got)
	}
	if got := dockerapi.AccessOf(dockertest.Unavailable(t)); got != dockerapi.AccessUnavailable {
		t.Fatalf("unavailable: %q", got)
	}
	if got := dockerapi.AccessOf(daemonError("x", cerrdefs.ErrNotFound)); got != "" {
		t.Fatalf("api error: %q", got)
	}
}

func TestNamePattern(t *testing.T) {
	for _, ok := range []string{"web", "db-1", "a.b_c", "X9"} {
		if !dockerapi.NamePattern.MatchString(ok) {
			t.Errorf("%q must be valid", ok)
		}
	}
	for _, bad := range []string{"", "a", "-web", "web app", "web/1", "ä"} {
		if dockerapi.NamePattern.MatchString(bad) {
			t.Errorf("%q must be invalid", bad)
		}
	}
}

func TestIsAnonymousVolume(t *testing.T) {
	hash := "3f2a" + strings.Repeat("0", 60)
	if !dockerapi.IsAnonymousVolume(hash, nil) || !dockerapi.IsAnonymousVolume("x", map[string]string{"com.docker.volume.anonymous": ""}) {
		t.Fatal("anonymous volumes not recognised")
	}
	if dockerapi.IsAnonymousVolume("web-data", map[string]string{"app": "web"}) {
		t.Fatal("a named volume is not anonymous")
	}
}

func TestSafeID(t *testing.T) {
	hex := strings.Repeat("0123456789abcdef", 4)
	for _, ok := range []string{"web", "db-1", "a.b_c", "a", "X9", "0123456789ab", hex, "web.1"} {
		if !dockerapi.SafeID(ok) {
			t.Errorf("%q must be safe", ok)
		}
	}
	for _, bad := range []string{"", ".", "..", ".hidden", "-web", "_web", "web/..", "../images/x", "web/1", "we b", "web?all=1", "web#frag", "web\n", "ä"} {
		if dockerapi.SafeID(bad) {
			t.Errorf("%q must be unsafe", bad)
		}
	}
}

// InvalidID is the one answer of every route with a container :id (the container routes and the console).
func TestInvalidID(t *testing.T) {
	var apiErr *sdk.APIError
	if !errors.As(dockerapi.InvalidID(), &apiErr) {
		t.Fatalf("InvalidID = %v", dockerapi.InvalidID())
	}
	if apiErr.Status != 400 || apiErr.Code != "invalid_request" || apiErr.Message != "id must be the name or the ID of a container" ||
		apiErr.Details["field"] != "id" || len(apiErr.Details) != 1 {
		t.Fatalf("InvalidID = %+v", apiErr)
	}
}

func TestSafeRef(t *testing.T) {
	for _, ok := range []string{"nginx:1.29", "ghcr.io/octo/app:v1.2", "localhost:5000/test/busybox:1.37", "my..image", "my.image", "a/.b",
		"nginx@sha256:" + strings.Repeat("d", 64)} {
		if !dockerapi.SafeRef(ok) {
			t.Errorf("%q must be safe", ok)
		}
	}
	for _, bad := range []string{"/etc/passwd", "../containers/abc", "nginx/../../containers/abc", "..", "nginx/..", "nginx?all=1", "nginx#frag",
		".", "./nginx", "nginx/.", "octo/./app"} {
		if dockerapi.SafeRef(bad) {
			t.Errorf("%q must be unsafe", bad)
		}
	}
}
