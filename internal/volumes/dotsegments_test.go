package volumes

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// Since Fiber 3.6 / fasthttp 1.75 the request path is cleaned (RFC 3986 dot-segment removal) before routing, so a "." or
// ".." segment in the id position no longer reaches a handler as an id. The property that matters, and that this test
// pins, is that Docker never sees "." or ".." as an id or name (a traversal inside the daemon URL path).
func TestDotSegmentNamesNeverReachDocker(t *testing.T) {
	var names []string
	f := fakeWith(nil, nil)
	f.VolumeRemoveFn = func(_ context.Context, name string, _ client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
		names = append(names, strings.Clone(name))
		return client.VolumeRemoveResult{}, nil
	}
	app := testApp(f)
	singleDots := []string{".", "%2e"}
	doubleDots := []string{"..", "%2e%2e", "%2E%2E", ".%2e", "%2e."}
	for _, id := range singleDots { // the cleaned path is /volumes, which has no DELETE
		if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/volumes/"+id, nil, anna)); resp.StatusCode != 405 {
			t.Errorf("DELETE /volumes/%s: %d, want 405", id, resp.StatusCode)
		}
	}
	for _, id := range doubleDots {
		if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/volumes/"+id, nil, anna)); resp.StatusCode != 404 {
			t.Errorf("DELETE /volumes/%s: %d, want 404", id, resp.StatusCode)
		}
	}
	for _, n := range names {
		if u, err := url.PathUnescape(n); err != nil || u == "." || u == ".." {
			t.Errorf("Docker was called with the name %q", n)
		}
	}
	// Positive control: an ordinary name reaches Docker on the same route.
	names = nil
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/volumes/web", nil, anna)); resp.StatusCode != 204 || len(names) != 1 || names[0] != "web" {
		t.Errorf("DELETE /volumes/web: %d, Docker called with %q", resp.StatusCode, names)
	}
}
