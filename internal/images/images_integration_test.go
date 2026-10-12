//go:build integration

package images

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss-ext-docker/internal/registries"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

func pullAndWait(t *testing.T, p *Pulls, ref string) Progress {
	t.Helper()
	pl, err := p.Start(ref)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(2 * time.Minute)
	for {
		snap, changed := pl.Watch()
		if snap.Done {
			return snap
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("pull of %s did not finish", ref)
		}
	}
}

func TestImagesAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	app, pulls := testApp(t, cli)
	if snap := pullAndWait(t, pulls, dockertest.Busybox); snap.Error != "" {
		t.Fatalf("pull: %+v", snap)
	}
	id := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: dockertest.Name(t, "img"), Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sleep", "300"}}})
	list := sdktest.DecodeJSON[[]Image](t, sdktest.Do(t, app, sdktest.Request("GET", "/images", nil, anna)))
	var busybox Image
	for _, im := range list {
		for _, tag := range im.Tags {
			if tag == dockertest.Busybox {
				busybox = im
			}
		}
	}
	if len(busybox.UsedBy) == 0 || busybox.UsedBy[0].ID != id {
		t.Fatalf("busybox not shown as used: %+v", busybox)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/images/"+busybox.ID, nil, anna)); resp.StatusCode != 409 {
		t.Fatalf("delete in use: %d", resp.StatusCode)
	}
}

func TestPullWithLogin(t *testing.T) {
	cli := dockertest.Live(t)
	reg := dockertest.Registry(t, cli, 5102, "alice", "wonderland")
	ref := dockertest.PushBusybox(t, cli, reg, "alice", "wonderland")
	_, pulls := testApp(t, cli)
	if snap := pullAndWait(t, pulls, ref); snap.Error == "" {
		t.Fatal("a pull without the stored login must fail")
	}
	if _, err := pulls.store.Add(registries.Input{Name: "test", Address: reg, Auth: true, Username: "alice", Password: "wonderland"}); err != nil {
		t.Fatal(err)
	}
	if snap := pullAndWait(t, pulls, ref); snap.Error != "" {
		t.Fatalf("pull with login: %+v", snap)
	}
	_, _ = cli.ImageRemove(context.Background(), ref, client.ImageRemoveOptions{PruneChildren: true})
}

func TestPruneAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	app, _ := testApp(t, cli)
	dockertest.Pull(t, cli, dockertest.Busybox)
	// the count is that of the unused images the list shows (the confirmation of the UI), not of every deleted layer
	unused := 0
	for _, im := range sdktest.DecodeJSON[[]Image](t, sdktest.Do(t, app, sdktest.Request("GET", "/images", nil, anna))) {
		if len(im.UsedBy) == 0 {
			unused++
		}
	}
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/images/prune", nil, anna))
	got := sdktest.DecodeJSON[struct {
		Deleted int `json:"deleted"`
	}](t, resp)
	if resp.StatusCode != 200 || got.Deleted != unused || unused == 0 {
		t.Fatalf("prune: %d, %d deleted, want the %d unused images of the list", resp.StatusCode, got.Deleted, unused)
	}
	if _, err := cli.ImageInspect(context.Background(), dockertest.Busybox); err == nil || !strings.Contains(err.Error(), "No such image") {
		t.Fatalf("busybox (unused) must be pruned: %v", err)
	}
}
