package images

import (
	"context"
	"net/url"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// Since Fiber 3.6 / fasthttp 1.75 the request path is cleaned (RFC 3986 dot-segment removal) before routing, so a "." or
// ".." segment in the id position no longer reaches a handler as an id. The property that matters, and that this test
// pins, is that Docker never sees "." or ".." as an id or name (a traversal inside the daemon URL path).
func TestDotSegmentIdsNeverReachDocker(t *testing.T) {
	var ids []string
	f := &dockertest.Fake{
		ContainerListFn: func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
			return client.ContainerListResult{}, nil
		},
		ImageRemoveFn: func(_ context.Context, id string, _ client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
			ids = append(ids, strings.Clone(id))
			return client.ImageRemoveResult{}, nil
		},
	}
	app, _ := testApp(t, f)
	singleDots := []string{".", "%2e"}
	doubleDots := []string{"..", "%2e%2e", "%2E%2E", ".%2e", "%2e."}
	for _, id := range singleDots { // the cleaned paths are /images (no DELETE) and /pulls/events (no route)
		if got := sdktest.Do(t, app, sdktest.Request("DELETE", "/images/"+id, nil, anna)).StatusCode; got != 405 {
			t.Errorf("DELETE /images/%s: %d, want 405", id, got)
		}
		if got := sdktest.Do(t, app, sdktest.Request("GET", "/pulls/"+id+"/events", nil, anna)).StatusCode; got != 404 {
			t.Errorf("GET /pulls/%s/events: %d, want 404", id, got)
		}
	}
	for _, id := range doubleDots {
		if got := sdktest.Do(t, app, sdktest.Request("DELETE", "/images/"+id, nil, anna)).StatusCode; got != 404 {
			t.Errorf("DELETE /images/%s: %d, want 404", id, got)
		}
		if got := sdktest.Do(t, app, sdktest.Request("GET", "/pulls/"+id+"/events", nil, anna)).StatusCode; got != 404 {
			t.Errorf("GET /pulls/%s/events: %d, want 404", id, got)
		}
	}
	for _, id := range ids {
		if u, err := url.PathUnescape(id); err != nil || u == "." || u == ".." {
			t.Errorf("Docker was called with the id %q", id)
		}
	}
	// Positive control: an ordinary id reaches Docker on the same route.
	ids = nil
	good := "sha256:" + strings.Repeat("ab", 32) // this route takes only a full image ID
	sdktest.Do(t, app, sdktest.Request("DELETE", "/images/"+good, nil, anna))
	if len(ids) == 0 || ids[0] != good {
		t.Errorf("DELETE /images/%s: Docker called with %q", good, ids)
	}
}
