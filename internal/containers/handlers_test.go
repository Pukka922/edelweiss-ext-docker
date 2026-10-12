package containers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

var anna = sdk.User{Name: "anna", UID: 1000}

func testApp(d dockerapi.Docker) *fiber.App { return testAppOf(New(d)) }

// testAppOf serves s, for tests that change its settings (StopTimeout, StatsInterval).
func testAppOf(s *Service) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, s)
	return app
}

func readAll(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestList(t *testing.T) {
	f := &dockertest.Fake{ContainerListFn: func(_ context.Context, o client.ContainerListOptions) (client.ContainerListResult, error) {
		if !o.All {
			t.Error("stopped containers must be listed too")
		}
		return client.ContainerListResult{Items: []container.Summary{
			{ID: "bbbbbbbbbbbbbbbb", Names: []string{"/web"}, Image: "nginx:1.29", State: container.StateRunning, Status: "Up 2 hours",
				Ports: []container.PortSummary{
					{IP: netip.MustParseAddr("0.0.0.0"), PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
					{IP: netip.MustParseAddr("::"), PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
					{PrivatePort: 443, Type: "tcp"},
				}},
			{ID: "aaaaaaaaaaaaaaaa", Names: []string{"/db"}, Image: "postgres:17", State: container.StateExited},
		}}, nil
	}}
	got := sdktest.DecodeJSON[[]Summary](t, sdktest.Do(t, testApp(f), sdktest.Request("GET", "/containers", nil, anna)))
	if len(got) != 2 || got[0].Name != "db" || got[1].Name != "web" || got[1].State != "running" {
		t.Fatalf("containers %+v (sorted by name)", got)
	}
	if ports := got[1].Ports; len(ports) != 2 || ports[0] != (PublishedPort{HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}) || ports[1].HostPort != 0 {
		t.Fatalf("ports %+v (IPv4 and IPv6 of one binding are one entry)", ports)
	}
	if got[0].Ports == nil || got[0].Networks == nil {
		t.Fatal("empty lists must be [] in JSON")
	}
}

func TestGet(t *testing.T) {
	c, img := inspected()
	f := &dockertest.Fake{
		ContainerInspectFn: func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: c}, nil
		},
		ImageInspectFn: func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{InspectResponse: *img}, nil
		},
	}
	got := sdktest.DecodeJSON[Info](t, sdktest.Do(t, testApp(f), sdktest.Request("GET", "/containers/web", nil, anna)))
	if got.Container.Name != "web" || !got.Container.State.Running || got.Spec.Network != "front" || len(got.Spec.Env) != 1 {
		t.Fatalf("info %+v", got)
	}
	var back Endpoint
	for _, n := range got.Container.Networks {
		if n.Name == "back" {
			back = n
		}
	}
	if len(got.Container.Networks) != 2 || got.Container.Networks[0].Name != "back" || len(got.Container.Env) != 3 || back.Aliases == nil {
		t.Fatalf("detail %+v", got.Container)
	}
}

func TestCreate(t *testing.T) {
	var created client.ContainerCreateOptions
	started := ""
	startErr := error(nil)
	f := &dockertest.Fake{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "nginx:1.29" {
				return client.ImageInspectResult{}, nil
			}
			return client.ImageInspectResult{}, cerrdefs.ErrNotFound
		},
		ContainerCreateFn: func(_ context.Context, o client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
			created = o
			return client.ContainerCreateResult{ID: "new-id"}, nil
		},
		ContainerStartFn: func(_ context.Context, id string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
			started = id
			return client.ContainerStartResult{}, startErr
		},
	}
	app := testApp(f)
	body := `{"name":"web","image":"nginx:1.29","ports":[{"containerPort":80,"hostPort":8080}],` +
		`"mounts":[{"type":"volume","source":"web-data","target":"/data"}],"env":[{"name":"TZ","value":"UTC"}],"start":true}`
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(body), anna))
	if got := sdktest.DecodeJSON[CreateResult](t, resp); resp.StatusCode != 201 || got.ID != "new-id" || started != "new-id" || created.Name != "web" {
		t.Fatalf("create: %d %+v started %q", resp.StatusCode, got, started)
	}

	missing := strings.Replace(body, "nginx:1.29", "nginx:9.9", 1)
	resp = sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(missing), anna))
	if resp.StatusCode != 404 || !strings.Contains(readAll(t, resp), "image_not_found") {
		t.Fatalf("missing image: %d", resp.StatusCode)
	}
	invalid := strings.Replace(body, `"target":"/data"`, `"target":"data"`, 1)
	resp = sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(invalid), anna))
	if raw := readAll(t, resp); resp.StatusCode != 400 || !strings.Contains(raw, `"field":"mounts.0.target"`) {
		t.Fatalf("invalid spec: %d %s", resp.StatusCode, raw)
	}

	// the field names the row of the request: the invalid port is the second one, not the first of a sorted list
	rows := `{"name":"web","image":"nginx:1.29","ports":[{"containerPort":80,"hostPort":8080},{"containerPort":0}]}`
	resp = sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(rows), anna))
	if raw := readAll(t, resp); resp.StatusCode != 400 || !strings.Contains(raw, `"field":"ports.1.containerPort"`) {
		t.Fatalf("invalid row: %d %s", resp.StatusCode, raw)
	}

	startErr = errors.New("Error response from daemon: port is already allocated")
	resp = sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(body), anna))
	if got := sdktest.DecodeJSON[CreateResult](t, resp); resp.StatusCode != 201 || !strings.Contains(got.Warning, "port is already allocated") {
		t.Fatalf("start failure: %d %+v", resp.StatusCode, got)
	}
}

// An omitted "start" means true (the form's default); only an explicit false creates the container without starting it.
func TestCreateStartsUnlessToldOtherwise(t *testing.T) {
	var starts int
	f := &dockertest.Fake{
		ImageInspectFn: func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			return client.ImageInspectResult{}, nil
		},
		ContainerCreateFn: func(context.Context, client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
			return client.ContainerCreateResult{ID: "new-id"}, nil
		},
		ContainerStartFn: func(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error) {
			starts++
			return client.ContainerStartResult{}, nil
		},
	}
	app := testApp(f)
	for _, tc := range []struct {
		name, body string
		starts     int
	}{
		{"omitted", `{"name":"web","image":"nginx:1.29"}`, 1},
		{"true", `{"name":"web","image":"nginx:1.29","start":true}`, 1},
		{"false", `{"name":"web","image":"nginx:1.29","start":false}`, 0},
	} {
		starts = 0
		resp := sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(tc.body), anna))
		if resp.StatusCode != 201 || starts != tc.starts {
			t.Errorf("start %s: status %d, %d starts, want %d", tc.name, resp.StatusCode, starts, tc.starts)
		}
	}
}

func TestActionsAndRemove(t *testing.T) {
	var calls []string
	running := true
	f := &dockertest.Fake{
		ContainerStartFn: func(_ context.Context, id string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
			calls = append(calls, "start "+id)
			return client.ContainerStartResult{}, nil
		},
		ContainerStopFn: func(_ context.Context, id string, o client.ContainerStopOptions) (client.ContainerStopResult, error) {
			calls = append(calls, "stop "+id)
			if o.Timeout == nil || *o.Timeout != 10 {
				t.Errorf("stop timeout %v", o.Timeout)
			}
			return client.ContainerStopResult{}, nil
		},
		ContainerRestartFn: func(_ context.Context, id string, o client.ContainerRestartOptions) (client.ContainerRestartResult, error) {
			calls = append(calls, "restart "+id)
			if o.Timeout == nil || *o.Timeout != 10 {
				t.Errorf("restart timeout %v", o.Timeout)
			}
			return client.ContainerRestartResult{}, nil
		},
		ContainerInspectFn: func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: container.InspectResponse{ID: id + "-full", State: &container.State{Running: running}}}, nil
		},
		ContainerRemoveFn: func(_ context.Context, id string, o client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
			if o.RemoveVolumes {
				calls = append(calls, "remove+volumes "+id)
			} else {
				calls = append(calls, "remove "+id)
			}
			return client.ContainerRemoveResult{}, nil
		},
	}
	app := testApp(f)
	for _, path := range []string{"/containers/web/start", "/containers/web/stop", "/containers/web/restart"} {
		if resp := sdktest.Do(t, app, sdktest.Request("POST", path, nil, anna)); resp.StatusCode != 204 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/containers/web?volumes=1", nil, anna)); resp.StatusCode != 204 {
		t.Fatalf("delete of a running container: %d", resp.StatusCode)
	}
	running = false
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/containers/db", nil, anna)); resp.StatusCode != 204 {
		t.Fatalf("delete of a stopped container: %d", resp.StatusCode)
	}
	want := []string{"start web", "stop web", "restart web", "stop web-full", "remove+volumes web-full", "remove db-full"}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v, want %v", calls, want)
	}
}

// A recreated container mounts the anonymous volumes of the old one by name, and Docker's own removal of the volumes
// leaves such mounts alone: with volumes=1 the handler removes the container's anonymous volumes itself, after the
// container. Named volumes stay; a volume that is gone already or still used by another container is no error.
func TestRemoveWithVolumesRemovesTheAnonymousVolumes(t *testing.T) {
	anon, gone, shared := strings.Repeat("a1", 32), strings.Repeat("b2", 32), strings.Repeat("c3", 32)
	var calls []string
	volumeErr := map[string]error{gone: cerrdefs.ErrNotFound, shared: cerrdefs.ErrConflict}
	f := &dockertest.Fake{
		ContainerInspectFn: func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: container.InspectResponse{ID: id + "-full", State: &container.State{},
				Mounts: []container.MountPoint{
					{Type: mount.TypeVolume, Name: anon, Destination: "/cache"},
					{Type: mount.TypeVolume, Name: "web-data", Destination: "/data"},
					{Type: mount.TypeBind, Source: "/srv/conf", Destination: "/conf"},
					{Type: mount.TypeVolume, Name: gone, Destination: "/gone"},
					{Type: mount.TypeVolume, Name: shared, Destination: "/shared"},
				}}}, nil
		},
		ContainerRemoveFn: func(_ context.Context, id string, _ client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
			calls = append(calls, "remove "+id)
			return client.ContainerRemoveResult{}, nil
		},
		VolumeRemoveFn: func(_ context.Context, name string, o client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
			calls = append(calls, "volume "+name)
			if o.Force {
				t.Errorf("volume %s removed with force", name)
			}
			return client.VolumeRemoveResult{}, volumeErr[name]
		},
	}
	app := testApp(f)
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/containers/web?volumes=1", nil, anna)); resp.StatusCode != 204 {
		t.Fatalf("delete with volumes: %d %s", resp.StatusCode, readAll(t, resp))
	}
	want := []string{"remove web-full", "volume " + anon, "volume " + gone, "volume " + shared}
	if strings.Join(calls, ",") != strings.Join(want, ",") {
		t.Fatalf("calls %v, want %v", calls, want)
	}

	calls = nil
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/containers/web", nil, anna)); resp.StatusCode != 204 || len(calls) != 1 {
		t.Fatalf("delete without volumes: %d, calls %v", resp.StatusCode, calls)
	}

	// any other error is reported, with what was removed
	volumeErr[anon] = errors.New("Error response from daemon: driver failed")
	resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/containers/web?volumes=1", nil, anna))
	if raw := readAll(t, resp); resp.StatusCode != 502 || !strings.Contains(raw, "docker_error") || !strings.Contains(raw, "driver failed") ||
		!strings.Contains(raw, "the container was removed") || !strings.Contains(raw, anon) {
		t.Fatalf("volume error: %d %s", resp.StatusCode, raw)
	}
}

// The id of the route goes into the URL path of the daemon request, where the client's path.Join would clean "." and
// ".." into another endpoint (GET /containers/. would become GET /containers/json): such ids never reach Docker.
func TestRoutesRefuseUnsafeIds(t *testing.T) {
	var calls []string
	record := func(what, id string) { calls = append(calls, what+" "+strings.Clone(id)) }
	f := &dockertest.Fake{
		ContainerInspectFn: func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			record("inspect", id)
			return client.ContainerInspectResult{Container: container.InspectResponse{ID: id, State: &container.State{}}}, nil
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
	}
	app := testApp(f)
	routes := []struct{ method, suffix string }{
		{"GET", ""}, {"DELETE", ""}, {"POST", "/start"}, {"POST", "/stop"}, {"POST", "/restart"},
	}
	for _, id := range []string{".", "..", "..%2Fimages%2Fx", ".hidden", "-web", "web%2F..", "we%20b"} {
		for _, r := range routes {
			target := "/containers/" + id + r.suffix
			resp := sdktest.Do(t, app, sdktest.Request(r.method, target, nil, anna))
			if raw := readAll(t, resp); resp.StatusCode != 400 || !strings.Contains(raw, "invalid_request") || !strings.Contains(raw, `"field":"id"`) {
				t.Errorf("%s %s: %d %s", r.method, target, resp.StatusCode, raw)
			}
		}
	}
	if len(calls) != 0 {
		t.Fatalf("Docker must not be called for these ids, was called: %q", calls)
	}

	hex := strings.Repeat("0123456789abcdef", 4)
	for _, id := range []string{"web", "db-1", "a.b_c", "0123456789ab", hex} {
		for _, r := range routes {
			calls = nil
			target := "/containers/" + id + r.suffix
			resp := sdktest.Do(t, app, sdktest.Request(r.method, target, nil, anna))
			if resp.StatusCode/100 != 2 || len(calls) == 0 || !strings.HasSuffix(calls[0], " "+id) {
				t.Errorf("%s %s: %d, calls %q", r.method, target, resp.StatusCode, calls)
			}
		}
	}
}

// The image of a new container is looked up by its name, in a URL path: a ref like ../containers/web must not turn the
// lookup into an inspection of a container.
func TestCreateRefusesUnsafeImages(t *testing.T) {
	var inspected []string
	f := &dockertest.Fake{ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		inspected = append(inspected, strings.Clone(ref))
		return client.ImageInspectResult{}, nil
	}}
	app := testApp(f)
	for _, ref := range []string{"../containers/web", "/etc/passwd", "nginx/../../containers/web", "nginx?all=1", "nginx#frag"} {
		body := fmt.Sprintf(`{"name":"web","image":%q}`, ref) // plain ASCII: a Go string literal is valid JSON
		resp := sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(body), anna))
		if raw := readAll(t, resp); resp.StatusCode != 400 || !strings.Contains(raw, "invalid_container_spec") || !strings.Contains(raw, `"field":"image"`) {
			t.Errorf("image %q: %d %s", ref, resp.StatusCode, raw)
		}
	}
	if len(inspected) != 0 {
		t.Fatalf("ImageInspect must not be called for these images, was called with %q", inspected)
	}
}
