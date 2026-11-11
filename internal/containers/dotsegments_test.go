package containers

import (
	"context"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// Since Fiber 3.6 / fasthttp 1.75 the request path is cleaned (RFC 3986 dot-segment removal) before routing, so a "." or
// ".." segment in the id position no longer reaches a handler as an id: /containers/./logs IS /containers/logs, and
// /containers/.. is a 404. The property that matters, and that these tests pin, is that Docker never sees "." or ".."
// as an id (that would be a traversal inside the daemon's URL path).
var (
	singleDots = []string{".", "%2e"}
	doubleDots = []string{"..", "%2e%2e", "%2E%2E", ".%2e", "%2e."}
)

// dotFake records every Docker call that takes a container id as "<operation> <id>".
func dotFake(calls *[]string) *dockertest.Fake {
	record := func(op, id string) { *calls = append(*calls, op+" "+strings.Clone(id)) }
	return &dockertest.Fake{
		ContainerListFn: func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
			return client.ContainerListResult{}, nil
		},
		ContainerInspectFn: func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			record("inspect", id)
			return client.ContainerInspectResult{Container: container.InspectResponse{ID: id, State: &container.State{Running: true}, Config: &container.Config{}}}, nil
		},
		ContainerStartFn: func(_ context.Context, id string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
			record("start", id)
			return client.ContainerStartResult{}, nil
		},
		ContainerStopFn: func(_ context.Context, id string, _ client.ContainerStopOptions) (client.ContainerStopResult, error) {
			record("stop", id)
			return client.ContainerStopResult{}, nil
		},
		ContainerRestartFn: func(_ context.Context, id string, _ client.ContainerRestartOptions) (client.ContainerRestartResult, error) {
			record("restart", id)
			return client.ContainerRestartResult{}, nil
		},
		ContainerRemoveFn: func(_ context.Context, id string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
			record("remove", id)
			return client.ContainerRemoveResult{}, nil
		},
		ContainerRenameFn: func(_ context.Context, id string, _ client.ContainerRenameOptions) (client.ContainerRenameResult, error) {
			record("rename", id)
			return client.ContainerRenameResult{}, nil
		},
		ContainerUpdateFn: func(_ context.Context, id string, _ client.ContainerUpdateOptions) (client.ContainerUpdateResult, error) {
			record("update", id)
			return client.ContainerUpdateResult{}, nil
		},
		ContainerLogsFn: func(_ context.Context, id string, _ client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
			record("logs", id)
			return io.NopCloser(strings.NewReader("")), nil
		},
		ContainerStatsFn: func(_ context.Context, id string, _ client.ContainerStatsOptions) (client.ContainerStatsResult, error) {
			record("stats", id)
			return client.ContainerStatsResult{Body: io.NopCloser(strings.NewReader(""))}, nil
		},
	}
}

func TestDotSegmentIdsNeverReachDocker(t *testing.T) {
	var calls []string
	app := testApp(dotFake(&calls))
	routes := []struct {
		method, suffix, op string
		single             int // the status today for "." and "%2e": the cleaned path is another route of /containers
	}{
		{"GET", "", "inspect", 200}, // /containers: the list
		{"PUT", "", "", 405},
		{"DELETE", "", "remove", 405},
		{"POST", "/start", "start", 405},
		{"POST", "/stop", "stop", 405},
		{"POST", "/restart", "restart", 405},
		{"GET", "/logs", "logs", 200},   // /containers/logs: the container named logs
		{"GET", "/stats", "stats", 200}, // /containers/stats: the container named stats
	}
	status := func(method, target string) (int, string) {
		resp := sdktest.Do(t, app, sdktest.Request(method, target, strings.NewReader("{}"), anna))
		return resp.StatusCode, readAll(t, resp)
	}
	for _, r := range routes {
		for _, id := range singleDots {
			if got, body := status(r.method, "/containers/"+id+r.suffix); got != r.single {
				t.Errorf("%s /containers/%s%s: %d %s, want %d", r.method, id, r.suffix, got, body, r.single)
			}
		}
		for _, id := range doubleDots {
			if got, body := status(r.method, "/containers/"+id+r.suffix); got != 404 {
				t.Errorf("%s /containers/%s%s: %d %s, want 404", r.method, id, r.suffix, got, body)
			}
		}
	}
	for _, c := range calls {
		id := c[strings.IndexByte(c, ' ')+1:]
		if u, err := url.PathUnescape(id); err != nil || u == "." || u == ".." || id == "." || id == ".." {
			t.Errorf("Docker was called with %q", c)
		}
	}

	// Positive control: an ordinary id reaches Docker on every one of these routes, so the checks above cannot pass
	// because nothing ever gets through.
	for _, r := range routes {
		calls = nil
		resp := sdktest.Do(t, app, sdktest.Request(r.method, "/containers/web"+r.suffix, strings.NewReader("{}"), anna))
		if r.suffix == "/logs" || r.suffix == "/stats" {
			_, _ = sseEvents(t, resp.Body)
		} else {
			_ = readAll(t, resp)
		}
		if r.op == "" { // PUT: the body of {} is not a valid edit; the route is reached all the same
			if resp.StatusCode == 404 || resp.StatusCode == 405 {
				t.Errorf("%s /containers/web%s: %d, the route is not reached", r.method, r.suffix, resp.StatusCode)
			}
			continue
		}
		if len(calls) == 0 || calls[len(calls)-1] != r.op+" web" && calls[0] != r.op+" web" {
			t.Errorf("%s /containers/web%s: %d, calls %q, want %q", r.method, r.suffix, resp.StatusCode, calls, r.op+" web")
		}
	}

	// ./logs and ./stats are exactly the cleaned requests /containers/logs and /containers/stats: same status, same body.
	for _, name := range []string{"logs", "stats"} {
		want, wantBody := status("GET", "/containers/"+name)
		got, gotBody := status("GET", "/containers/./"+name)
		if got != want || gotBody != wantBody {
			t.Errorf("GET /containers/./%s: %d %s\nbut GET /containers/%s: %d %s", name, got, gotBody, name, want, wantBody)
		}
	}
}
