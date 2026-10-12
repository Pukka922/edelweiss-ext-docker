package volumes

import (
	"context"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

var anna = sdk.User{Name: "anna", UID: 1000}

func testApp(d dockerapi.Docker) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, d)
	return app
}

func fakeWith(vols []volume.Volume, users []container.Summary) *dockertest.Fake {
	return &dockertest.Fake{
		VolumeListFn: func(context.Context, client.VolumeListOptions) (client.VolumeListResult, error) {
			return client.VolumeListResult{Items: vols}, nil
		},
		ContainerListFn: func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
			return client.ContainerListResult{Items: users}, nil
		},
	}
}

func TestList(t *testing.T) {
	hash := strings.Repeat("ab", 32)
	f := fakeWith(
		[]volume.Volume{{Name: "web-data", Driver: "local"}, {Name: hash, Driver: "local", Labels: map[string]string{"com.docker.volume.anonymous": ""}}},
		[]container.Summary{{ID: "c1aaaaaaaaaaaaaaaa", Names: []string{"/web"}, Mounts: []container.MountPoint{{Type: mount.TypeVolume, Name: "web-data"}}}},
	)
	got := sdktest.DecodeJSON[[]Volume](t, sdktest.Do(t, testApp(f), sdktest.Request("GET", "/volumes", nil, anna)))
	if len(got) != 2 || got[0].Name != hash || !got[0].Anonymous || got[1].Name != "web-data" || len(got[1].UsedBy) != 1 || got[1].Options == nil {
		t.Fatalf("volumes %+v", got)
	}
}

func TestCreate(t *testing.T) {
	var opts client.VolumeCreateOptions
	f := &dockertest.Fake{VolumeCreateFn: func(_ context.Context, o client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
		opts = o
		return client.VolumeCreateResult{Volume: volume.Volume{Name: o.Name, Driver: o.Driver, Options: o.DriverOpts}}, nil
	}}
	body := `{"name":"nfs-share","options":{"type":"nfs","o":"addr=10.0.0.5,rw","device":":/export"}}`
	resp := sdktest.Do(t, testApp(f), sdktest.Request("POST", "/volumes", strings.NewReader(body), anna))
	if resp.StatusCode != 201 || opts.Driver != "local" || opts.DriverOpts["type"] != "nfs" {
		t.Fatalf("create: %d %+v", resp.StatusCode, opts)
	}
	for _, bad := range []string{`{"name":"a b"}`, `{"name":"x"}`, `{"name":"ok-name","options":{"":"v"}}`} {
		if resp := sdktest.Do(t, testApp(f), sdktest.Request("POST", "/volumes", strings.NewReader(bad), anna)); resp.StatusCode != 400 {
			t.Errorf("%s: %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestDelete(t *testing.T) {
	removed := ""
	f := fakeWith(nil, []container.Summary{{ID: "c1aaaaaaaaaaaaaaaa", Names: []string{"/web"}, Mounts: []container.MountPoint{{Type: mount.TypeVolume, Name: "web-data"}}}})
	f.VolumeRemoveFn = func(_ context.Context, name string, o client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
		if o.Force {
			t.Error("volumes are never removed by force")
		}
		removed = name
		return client.VolumeRemoveResult{}, nil
	}
	used := sdktest.Do(t, testApp(f), sdktest.Request("DELETE", "/volumes/web-data", nil, anna))
	code := sdktest.DecodeJSON[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, used).Error.Code
	if used.StatusCode != 409 || code != "volume_in_use" || removed != "" {
		t.Fatalf("in use: %d %q, removed %q", used.StatusCode, code, removed)
	}
	free := sdktest.Do(t, testApp(f), sdktest.Request("DELETE", "/volumes/old-data", nil, anna))
	if free.StatusCode != 204 || removed != "old-data" {
		t.Fatalf("free: %d, removed %q", free.StatusCode, removed)
	}
}
