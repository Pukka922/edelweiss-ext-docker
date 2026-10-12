//go:build integration

package containers

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestCreateAndDetailsAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	dockertest.Pull(t, cli, dockertest.Busybox)
	// sleep as PID 1 ignores SIGTERM, so every stop runs into the stop timeout, and sdktest.Do gives up after 5 s
	svc := New(cli)
	svc.StopTimeout = 1
	app := testAppOf(svc)
	name, vol := dockertest.Name(t, "web"), dockertest.Name(t, "data")
	dockertest.RemoveVolumeAtEnd(t, cli, vol)
	spec := Spec{Name: name, Image: dockertest.Busybox, Command: "sleep 300",
		Ports:   []Port{{HostPort: 18080, ContainerPort: 80, Protocol: "tcp"}},
		Mounts:  []Mount{{Type: "volume", Source: vol, Target: "/data"}},
		Env:     []EnvVar{{Name: "TZ", Value: "UTC"}},
		Restart: Restart{Policy: "no"}, Network: "bridge"}.Normalize()
	body := toJSON(t, Spec{Name: spec.Name, Image: spec.Image, Command: spec.Command, Ports: spec.Ports, Mounts: spec.Mounts,
		Env: spec.Env, Network: spec.Network, Restart: spec.Restart, Start: true})
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(body), anna))
	created := sdktest.DecodeJSON[CreateResult](t, resp)
	if resp.StatusCode != 201 || created.Warning != "" {
		t.Fatalf("create: %d %+v", resp.StatusCode, created)
	}
	dockertest.RemoveContainerAtEnd(t, cli, created.ID)

	info := sdktest.DecodeJSON[Info](t, sdktest.Do(t, app, sdktest.Request("GET", "/containers/"+created.ID, nil, anna)))
	if !info.Container.State.Running {
		t.Fatalf("not running: %+v", info.Container.State)
	}
	if !reflect.DeepEqual(info.Spec, spec) {
		t.Fatalf("the form of the new container differs from what was sent:\n got %+v\nwant %+v", info.Spec, spec)
	}

	for _, action := range []string{"stop", "start", "restart"} {
		if resp := sdktest.Do(t, app, sdktest.Request("POST", "/containers/"+created.ID+"/"+action, nil, anna)); resp.StatusCode != 204 {
			t.Fatalf("%s: %d", action, resp.StatusCode)
		}
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/containers/"+created.ID+"?volumes=1", nil, anna)); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := cli.ContainerInspect(context.Background(), created.ID, client.ContainerInspectOptions{}); err == nil {
		t.Fatal("the container still exists")
	}
}

// The labels of the form reach the daemon, with their empty values and their case; a label in Docker's namespaces is refused.
func TestCreateWithLabelsAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	dockertest.Pull(t, cli, dockertest.Busybox)
	app := testApp(cli)
	name := dockertest.Name(t, "labels")
	body := toJSON(t, Spec{Name: name, Image: dockertest.Busybox, Command: "sleep 300",
		Labels: []Label{{Key: "tier", Value: "front"}, {Key: "App", Value: "web"}, {Key: "empty", Value: ""}}, Start: true})
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(body), anna))
	res := sdktest.DecodeJSON[CreateResult](t, resp)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %+v", resp.StatusCode, res)
	}
	dockertest.RemoveContainerAtEnd(t, cli, res.ID)
	got, err := cli.ContainerInspect(context.Background(), res.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	labels := got.Container.Config.Labels
	if empty, ok := labels["empty"]; labels["tier"] != "front" || labels["App"] != "web" || !ok || empty != "" || len(labels["app"]) != 0 {
		t.Fatalf("labels of the new container %q", labels)
	}
	info := sdktest.DecodeJSON[Info](t, sdktest.Do(t, app, sdktest.Request("GET", "/containers/"+res.ID, nil, anna)))
	if want := []Label{{Key: "App", Value: "web"}, {Key: "empty"}, {Key: "tier", Value: "front"}}; !reflect.DeepEqual(info.Spec.Labels, want) {
		t.Fatalf("form labels %+v, want %+v", info.Spec.Labels, want)
	}

	reserved := toJSON(t, Spec{Name: dockertest.Name(t, "labels"), Image: dockertest.Busybox, Command: "sleep 300",
		Labels: []Label{{Key: "app", Value: "web"}, {Key: "com.docker.compose.project", Value: "e2e"}}})
	resp = sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(reserved), anna))
	if raw := readAll(t, resp); resp.StatusCode != 400 || !strings.Contains(raw, `"field":"labels.1.key"`) {
		t.Fatalf("a label of Docker's namespace: %d %s, want 400 for the field labels.1.key", resp.StatusCode, raw)
	}
}

func TestCreateWithLimitsAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	dockertest.Pull(t, cli, dockertest.Busybox)
	app := testApp(cli)
	name := dockertest.Name(t, "limits")
	body := toJSON(t, Spec{Name: name, Image: dockertest.Busybox, Command: "sleep 300",
		Resources: Resources{Memory: 64 << 20, CPUs: 0.5}, Start: true})
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/containers", strings.NewReader(body), anna))
	res := sdktest.DecodeJSON[CreateResult](t, resp)
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d %+v", resp.StatusCode, res)
	}
	dockertest.RemoveContainerAtEnd(t, cli, res.ID)
	got, err := cli.ContainerInspect(context.Background(), res.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if h := got.Container.HostConfig; h.Memory != 64<<20 || h.NanoCPUs != 500_000_000 {
		t.Fatalf("limits %d %d (warnings %v)", h.Memory, h.NanoCPUs, res.Warnings)
	}
	info := sdktest.DecodeJSON[Info](t, sdktest.Do(t, app, sdktest.Request("GET", "/containers/"+res.ID, nil, anna)))
	if info.Spec.Resources != (Resources{Memory: 64 << 20, CPUs: 0.5}) {
		t.Fatalf("form resources %+v", info.Spec.Resources)
	}
}
