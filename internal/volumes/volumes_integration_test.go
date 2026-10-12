//go:build integration

package volumes

import (
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

func TestVolumesAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	app := testApp(cli)
	name := dockertest.Name(t, "vol")
	dockertest.RemoveVolumeAtEnd(t, cli, name)
	if resp := sdktest.Do(t, app, sdktest.Request("POST", "/volumes", strings.NewReader(`{"name":"`+name+`"}`), anna)); resp.StatusCode != 201 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	dockertest.Run(t, cli, client.ContainerCreateOptions{Name: dockertest.Name(t, "user"),
		Config:     &container.Config{Image: dockertest.Busybox, Cmd: []string{"sleep", "300"}},
		HostConfig: &container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeVolume, Source: name, Target: "/data"}}}})
	list := sdktest.DecodeJSON[[]Volume](t, sdktest.Do(t, app, sdktest.Request("GET", "/volumes", nil, anna)))
	found := false
	for _, v := range list {
		if v.Name == name {
			found = len(v.UsedBy) == 1
		}
	}
	if !found {
		t.Fatalf("volume %s not listed as used: %+v", name, list)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/volumes/"+name, nil, anna)); resp.StatusCode != 409 {
		t.Fatalf("delete in use: %d", resp.StatusCode)
	}
}
