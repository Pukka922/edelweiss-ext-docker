package networks

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// Every route with a network :id checks it before Docker is called: an unsafe one answers 400 invalid_request, field "id".
func TestUnsafeIdsNeverReachDocker(t *testing.T) {
	var calls []string
	record := func(op, id string) { calls = append(calls, op+" "+strings.Clone(id)) }
	f := &dockertest.Fake{
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
	routes := []struct{ method, suffix string }{{"GET", ""}, {"DELETE", ""}, {"POST", "/connect"}, {"POST", "/disconnect"}}
	do := func(method, target string) (int, string) {
		resp := sdktest.Do(t, app, sdktest.Request(method, target, strings.NewReader(`{"container":"web"}`), anna))
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	for _, id := range []string{"%2F", "a%2Fb", "..%2F", "%5C", "a%20b", "-x", ".x", strings.Repeat("a", 300), "%00"} {
		for _, r := range routes {
			target := "/networks/" + id + r.suffix
			if code, raw := do(r.method, target); code != 400 || !strings.Contains(raw, "invalid_request") || !strings.Contains(raw, `"field":"id"`) {
				t.Errorf("%s %s: %d %s", r.method, target, code, raw)
			}
		}
	}
	if len(calls) != 0 {
		t.Fatalf("Docker must not be called for these ids, was called: %q", calls)
	}
	for _, id := range []string{"appnet", "a", "db-1", "a.b_c", strings.Repeat("0123456789abcdef", 4)} {
		for _, r := range routes {
			calls = nil
			target := "/networks/" + id + r.suffix
			if code, raw := do(r.method, target); code/100 != 2 || len(calls) == 0 || !strings.HasSuffix(calls[0], " "+id) {
				t.Errorf("%s %s: %d %s, calls %q", r.method, target, code, raw, calls)
			}
		}
	}
}
