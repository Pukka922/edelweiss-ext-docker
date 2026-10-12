package usage

import (
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
)

func TestFromContainers(t *testing.T) {
	idx := FromContainers([]container.Summary{
		{ID: "c2" + "0000000000000000", Names: []string{"/web"}, ImageID: "sha256:nginx",
			Mounts:          []container.MountPoint{{Type: mount.TypeVolume, Name: "web-data"}, {Type: mount.TypeBind, Source: "/srv"}},
			NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{"front": {}, "bridge": {}}}},
		{ID: "c1" + "0000000000000000", Names: []string{"/api"}, ImageID: "sha256:nginx",
			Mounts:          []container.MountPoint{{Type: mount.TypeVolume, Name: "web-data"}},
			NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{"front": {}}}},
	})
	if got := idx.Image("sha256:nginx"); len(got) != 2 || got[0].Name != "api" || got[1].Name != "web" {
		t.Fatalf("image users %+v (sorted by name)", got)
	}
	if got := idx.Volume("web-data"); len(got) != 2 {
		t.Fatalf("volume users %+v", got)
	}
	if got := idx.Network("front"); len(got) != 2 {
		t.Fatalf("network users %+v", got)
	}
	if got := idx.Volume("unused"); got == nil || len(got) != 0 {
		t.Fatalf("unused volume: %#v, want an empty non-nil slice", got)
	}
	if NameOf(container.Summary{ID: "0123456789abcdef"}) != "0123456789ab" {
		t.Fatal("a container without a name is shown by its short ID")
	}
}
