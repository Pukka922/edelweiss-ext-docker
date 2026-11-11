package networks

import (
	"context"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// Since Fiber 3.6 / fasthttp 1.75 the request path is cleaned (RFC 3986 dot-segment removal) before routing, so a "." or
// ".." segment in the id position no longer reaches a handler as an id. The property that matters, and that this test
// pins, is that Docker never sees "." or ".." as an id or name (a traversal inside the daemon URL path).
func TestDotSegmentIdsNeverReachDocker(t *testing.T) {
	var calls []string
	record := func(op, id string) { calls = append(calls, op+" "+strings.Clone(id)) }
	f := &dockertest.Fake{
		NetworkListFn: func(context.Context, client.NetworkListOptions) (client.NetworkListResult, error) {
			return client.NetworkListResult{}, nil
		},
		ContainerListFn: func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
			return client.ContainerListResult{}, nil
		},
		NetworkInspectFn: func(_ context.Context, id string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
			record("inspect", id)
			return client.NetworkInspectResult{Network: network.Inspect{Network: network.Network{ID: id, Name: id}}}, nil
		},
		NetworkRemoveFn: func(_ context.Context, id string, _ client.NetworkRemoveOptions) (client.NetworkRemoveResult, error) {
			record("remove", id)
			return client.NetworkRemoveResult{}, nil
		},
		NetworkConnectFn: func(_ context.Context, id string, _ client.NetworkConnectOptions) (client.NetworkConnectResult, error) {
			record("connect", id)
			return client.NetworkConnectResult{}, nil
		},
		NetworkDisconnectFn: func(_ context.Context, id string, _ client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error) {
			record("disconnect", id)
			return client.NetworkDisconnectResult{}, nil
		},
	}
	app := testApp(f)
	routes := []struct {
		method, suffix, op string
		single             int // the status today for "." and "%2e": the cleaned path is another route of /networks
	}{
		{"GET", "", "inspect", 200}, // /networks: the list
		{"DELETE", "", "remove", 405},
		{"POST", "/connect", "connect", 405},
		{"POST", "/disconnect", "disconnect", 405},
	}
	do := func(method, target string) int {
		return sdktest.Do(t, app, sdktest.Request(method, target, strings.NewReader("{\"container\":\"web\"}"), anna)).StatusCode
	}
	singleDots := []string{".", "%2e"}
	doubleDots := []string{"..", "%2e%2e", "%2E%2E", ".%2e", "%2e."}
	for _, r := range routes {
		for _, id := range singleDots {
			if got := do(r.method, "/networks/"+id+r.suffix); got != r.single {
				t.Errorf("%s /networks/%s%s: %d, want %d", r.method, id, r.suffix, got, r.single)
			}
		}
		for _, id := range doubleDots {
			if got := do(r.method, "/networks/"+id+r.suffix); got != 404 {
				t.Errorf("%s /networks/%s%s: %d, want 404", r.method, id, r.suffix, got)
			}
		}
	}
	for _, c := range calls {
		id := c[strings.IndexByte(c, ' ')+1:]
		if u, err := url.PathUnescape(id); err != nil || u == "." || u == ".." {
			t.Errorf("Docker was called with %q", c)
		}
	}
	// Positive control: an ordinary id reaches Docker on every one of these routes.
	for _, r := range routes {
		calls = nil
		do(r.method, "/networks/web"+r.suffix)
		if !slices.Contains(calls, r.op+" web") {
			t.Errorf("%s /networks/web%s: calls %q, want %q", r.method, r.suffix, calls, r.op+" web")
		}
	}
}
