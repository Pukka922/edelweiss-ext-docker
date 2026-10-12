//go:build integration

package containers

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

func TestEditAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	ctx := context.Background()
	// sleep as PID 1 ignores SIGTERM, so every stop runs into the stop timeout, and sdktest.Do gives up after 5 s
	svc := New(cli)
	svc.StopTimeout = 1
	app := testAppOf(svc)
	name, vol := dockertest.Name(t, "edit"), dockertest.Name(t, "editdata")
	dockertest.RemoveVolumeAtEnd(t, cli, vol)
	old := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: name,
		Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sleep", "300"}, Volumes: map[string]struct{}{"/cache": {}}},
		HostConfig: &container.HostConfig{
			Mounts:    []mount.Mount{{Type: mount.TypeVolume, Source: vol, Target: "/data"}},
			Resources: container.Resources{Memory: 64 << 20},
		}})
	dockertest.Exec(t, cli, old, "sh", "-c", "echo hi > /data/a && echo yo > /cache/b")
	oldHost := dockertest.Exec(t, cli, old, "hostname")

	info := sdktest.DecodeJSON[Info](t, sdktest.Do(t, app, sdktest.Request("GET", "/containers/"+old, nil, anna)))
	spec := info.Spec
	spec.Env = append(spec.Env, EnvVar{Name: "FOO", Value: "bar"})
	resp := sdktest.Do(t, app, sdktest.Request("PUT", "/containers/"+old, strings.NewReader(toJSON(t, spec)), anna))
	res := sdktest.DecodeJSON[EditResult](t, resp)
	if resp.StatusCode != 200 || !res.Recreated || res.ID == old || res.Warning != "" {
		t.Fatalf("recreate: %d %+v", resp.StatusCode, res)
	}
	dockertest.RemoveContainerAtEnd(t, cli, res.ID)

	got, err := cli.ContainerInspect(ctx, res.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	nc := got.Container
	if strings.TrimPrefix(nc.Name, "/") != name || nc.HostConfig.Memory != 64<<20 || !slices.Contains(nc.Config.Env, "FOO=bar") || !nc.State.Running {
		t.Fatalf("new container %+v", nc)
	}
	if data := dockertest.Exec(t, cli, res.ID, "cat", "/data/a", "/cache/b"); data != "hi\nyo\n" {
		t.Fatalf("volume data %q, want the files of the old container", data)
	}
	if host := dockertest.Exec(t, cli, res.ID, "hostname"); host == oldHost {
		t.Fatalf("hostname %q is still the old container's", host)
	}
	if _, err := cli.ContainerInspect(ctx, name+parkedSuffix, client.ContainerInspectOptions{}); err == nil {
		t.Fatal("the old container is still there")
	}

	// directly: new name and restart policy, the memory limit stays
	spec.Name, spec.Restart = name+"-2", Restart{Policy: "unless-stopped"}
	resp = sdktest.Do(t, app, sdktest.Request("PUT", "/containers/"+res.ID, strings.NewReader(toJSON(t, spec)), anna))
	direct := sdktest.DecodeJSON[EditResult](t, resp)
	if resp.StatusCode != 200 || direct.Recreated || direct.ID != res.ID {
		t.Fatalf("direct edit: %d %+v", resp.StatusCode, direct)
	}
	got, _ = cli.ContainerInspect(ctx, res.ID, client.ContainerInspectOptions{})
	if got.Container.Name != "/"+name+"-2" || got.Container.HostConfig.RestartPolicy.Name != container.RestartPolicyUnlessStopped ||
		got.Container.HostConfig.Memory != 64<<20 {
		t.Fatalf("after the direct edit %+v %+v", got.Container.Name, got.Container.HostConfig)
	}
}

// A new network in the form replaces the old one: the recreated container is connected to the form's network only.
func TestEditChangesTheNetworkAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	ctx := context.Background()
	svc := New(cli)
	svc.StopTimeout = 1
	app := testAppOf(svc)
	net := dockertest.Name(t, "editnet")
	created, err := cli.NetworkCreate(ctx, net, client.NetworkCreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	dockertest.RemoveNetworkAtEnd(t, cli, created.ID) // registered before the containers, so it runs after they are removed
	old := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: dockertest.Name(t, "netmove"),
		Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sleep", "300"}}})

	info := sdktest.DecodeJSON[Info](t, sdktest.Do(t, app, sdktest.Request("GET", "/containers/"+old, nil, anna)))
	if info.Spec.Network != "bridge" {
		t.Fatalf("network %q, want bridge", info.Spec.Network)
	}
	spec := info.Spec
	spec.Network = net
	resp := sdktest.Do(t, app, sdktest.Request("PUT", "/containers/"+old, strings.NewReader(toJSON(t, spec)), anna))
	res := sdktest.DecodeJSON[EditResult](t, resp)
	if resp.StatusCode != 200 || !res.Recreated || res.Warning != "" {
		t.Fatalf("recreate: %d %+v", resp.StatusCode, res)
	}
	dockertest.RemoveContainerAtEnd(t, cli, res.ID)
	got, err := cli.ContainerInspect(ctx, res.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var networks []string
	for name := range got.Container.NetworkSettings.Networks {
		networks = append(networks, name)
	}
	if string(got.Container.HostConfig.NetworkMode) != net || !slices.Equal(networks, []string{net}) {
		t.Fatalf("network mode %q, networks %v: want %s alone", got.Container.HostConfig.NetworkMode, networks, net)
	}
}

// The new container mounts the anonymous volumes of the old one by name; deleting it with its volumes still removes them.
func TestDeleteRemovesTheAnonymousVolumesOfARecreatedContainerAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	ctx := context.Background()
	svc := New(cli)
	svc.StopTimeout = 1
	app := testAppOf(svc)
	old := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: dockertest.Name(t, "anondel"),
		Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sleep", "300"}, Volumes: map[string]struct{}{"/cache": {}}}})

	info := sdktest.DecodeJSON[Info](t, sdktest.Do(t, app, sdktest.Request("GET", "/containers/"+old, nil, anna)))
	anon := ""
	for _, m := range info.Spec.Mounts {
		if m.Anonymous && m.Target == "/cache" {
			anon = m.Source
		}
	}
	if anon == "" {
		t.Fatalf("no anonymous volume on /cache in %+v", info.Spec.Mounts)
	}
	dockertest.RemoveVolumeAtEnd(t, cli, anon) // if the test fails; registered first, so it runs last
	spec := info.Spec
	spec.Env = append(spec.Env, EnvVar{Name: "FOO", Value: "bar"})
	resp := sdktest.Do(t, app, sdktest.Request("PUT", "/containers/"+old, strings.NewReader(toJSON(t, spec)), anna))
	res := sdktest.DecodeJSON[EditResult](t, resp)
	if resp.StatusCode != 200 || !res.Recreated || res.Warning != "" {
		t.Fatalf("recreate: %d %+v", resp.StatusCode, res)
	}
	dockertest.RemoveContainerAtEnd(t, cli, res.ID)
	if _, err := cli.VolumeInspect(ctx, anon, client.VolumeInspectOptions{}); err != nil {
		t.Fatalf("the anonymous volume is gone after the recreation: %v", err)
	}

	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/containers/"+res.ID+"?volumes=1", nil, anna)); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
	if _, err := cli.VolumeInspect(ctx, anon, client.VolumeInspectOptions{}); err == nil {
		t.Fatalf("the anonymous volume %s is still there after DELETE ?volumes=1", anon)
	}
}

// "Image neu laden" sends the form unchanged. While the ref names the image the container runs, nothing happens; once a
// pull has moved the ref to another image (here a commit of the container, tagged with the ref) the container is recreated
// from it.
func TestEditRecreatesForAMovedTagAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	ctx := context.Background()
	svc := New(cli)
	svc.StopTimeout = 1
	app := testAppOf(svc)
	dockertest.Pull(t, cli, dockertest.Busybox)
	ref := dockertest.Name(t, "img") + ":1"
	if _, err := cli.ImageTag(ctx, client.ImageTagOptions{Source: dockertest.Busybox, Target: ref}); err != nil {
		t.Fatal(err)
	}
	// registered before the containers, so it runs after they are removed
	t.Cleanup(func() { _, _ = cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{Force: true, PruneChildren: true}) })
	name := dockertest.Name(t, "moved")
	old := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: name,
		Config: &container.Config{Image: ref, Cmd: []string{"sleep", "300"}}})

	info := sdktest.DecodeJSON[Info](t, sdktest.Do(t, app, sdktest.Request("GET", "/containers/"+old, nil, anna)))
	edit := func(id string) EditResult {
		t.Helper()
		resp := sdktest.Do(t, app, sdktest.Request("PUT", "/containers/"+id, strings.NewReader(toJSON(t, info.Spec)), anna))
		res := sdktest.DecodeJSON[EditResult](t, resp)
		if resp.StatusCode != 200 || res.Warning != "" {
			t.Fatalf("PUT /containers/%s: %d %+v", id, resp.StatusCode, res)
		}
		return res
	}
	if same := edit(old); same.Recreated || same.ID != old {
		t.Fatalf("the ref still names the image of the container, yet: %+v", same)
	}

	committed, err := cli.ContainerCommit(ctx, old, client.ContainerCommitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = cli.ImageRemove(ctx, committed.ID, client.ImageRemoveOptions{Force: true, PruneChildren: true})
	})
	if _, err := cli.ImageTag(ctx, client.ImageTagOptions{Source: committed.ID, Target: ref}); err != nil {
		t.Fatal(err)
	}
	moved, err := cli.ImageInspect(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}

	res := edit(old)
	if !res.Recreated || res.ID == old {
		t.Fatalf("the ref names another image now, but: %+v", res)
	}
	dockertest.RemoveContainerAtEnd(t, cli, res.ID)
	got, err := cli.ContainerInspect(ctx, res.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Container.Image != moved.ID || !got.Container.State.Running || strings.TrimPrefix(got.Container.Name, "/") != name {
		t.Fatalf("new container: image %q (want %q), %+v", got.Container.Image, moved.ID, got.Container.State)
	}
	// the form is loaded again, as the UI does: the commit made "sleep 300" the command of the image, so the form of the new
	// container shows no command of its own
	info = sdktest.DecodeJSON[Info](t, sdktest.Do(t, app, sdktest.Request("GET", "/containers/"+res.ID, nil, anna)))
	if again := edit(res.ID); again.Recreated || again.ID != res.ID {
		t.Fatalf("the container runs the image of the ref now, yet: %+v", again)
	}
}
