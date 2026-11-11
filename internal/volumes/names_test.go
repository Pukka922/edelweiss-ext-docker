package volumes

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// Every route with a :name checks it before Docker is called: an unsafe name answers 400 invalid_request, field "name".
func TestUnsafeNamesNeverReachDocker(t *testing.T) {
	var calls []string
	record := func(op string) { calls = append(calls, op) }
	f := &dockertest.Fake{
		VolumeListFn: func(context.Context, client.VolumeListOptions) (client.VolumeListResult, error) {
			record("volume list")
			return client.VolumeListResult{}, nil
		},
		ContainerListFn: func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
			record("container list")
			return client.ContainerListResult{}, nil
		},
		VolumeRemoveFn: func(_ context.Context, name string, _ client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
			record("remove " + strings.Clone(name))
			return client.VolumeRemoveResult{}, nil
		},
	}
	app := testApp(f)
	for _, name := range []string{"%2F", "a%2Fb", "..%2F", "%5C", "a%20b", "-x", ".x", strings.Repeat("a", 300), "%00"} {
		target := "/volumes/" + name
		resp := sdktest.Do(t, app, sdktest.Request("DELETE", target, nil, anna))
		b, _ := io.ReadAll(resp.Body)
		if raw := string(b); resp.StatusCode != 400 || !strings.Contains(raw, "invalid_request") || !strings.Contains(raw, `"field":"name"`) {
			t.Errorf("DELETE %s: %d %s", target, resp.StatusCode, raw)
		}
	}
	if len(calls) != 0 {
		t.Fatalf("Docker must not be called for these names, was called: %q", calls)
	}
	for _, name := range []string{"web", "a", "db-1", "a.b_c", strings.Repeat("0123456789abcdef", 4)} {
		calls = nil
		resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/volumes/"+name, nil, anna))
		if resp.StatusCode != 204 || len(calls) == 0 || calls[len(calls)-1] != "remove "+name {
			t.Errorf("DELETE /volumes/%s: %d, calls %q", name, resp.StatusCode, calls)
		}
	}
}
