// Package usage tells which containers, running or not, use an image, a volume or a network.
package usage

import (
	"context"
	"slices"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
)

// Ref names a container.
type Ref struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Index maps images (by ID), volumes and networks (by name) to the containers that use them.
type Index struct {
	images, volumes, networks map[string][]Ref
}

// Load builds the index from all containers of the daemon.
func Load(ctx context.Context, d dockerapi.Docker) (Index, error) {
	res, err := d.ContainerList(ctx, client.ContainerListOptions{All: true})
	if err != nil {
		return Index{}, err
	}
	return FromContainers(res.Items), nil
}

// FromContainers builds the index from a container list.
func FromContainers(list []container.Summary) Index {
	idx := Index{images: map[string][]Ref{}, volumes: map[string][]Ref{}, networks: map[string][]Ref{}}
	for _, c := range list {
		ref := Ref{ID: c.ID, Name: NameOf(c)}
		idx.images[c.ImageID] = append(idx.images[c.ImageID], ref)
		for _, m := range c.Mounts {
			if m.Type == mount.TypeVolume && m.Name != "" {
				idx.volumes[m.Name] = append(idx.volumes[m.Name], ref)
			}
		}
		if c.NetworkSettings != nil {
			for name := range c.NetworkSettings.Networks {
				idx.networks[name] = append(idx.networks[name], ref)
			}
		}
	}
	for _, m := range []map[string][]Ref{idx.images, idx.volumes, idx.networks} {
		for _, refs := range m {
			slices.SortFunc(refs, func(a, b Ref) int { return strings.Compare(a.Name, b.Name) })
		}
	}
	return idx
}

// Image returns the containers of the image id.
func (i Index) Image(id string) []Ref { return orEmpty(i.images[id]) }

// Volume returns the containers that mount the volume name.
func (i Index) Volume(name string) []Ref { return orEmpty(i.volumes[name]) }

// Network returns the containers connected to the network name.
func (i Index) Network(name string) []Ref { return orEmpty(i.networks[name]) }

func orEmpty(refs []Ref) []Ref {
	if refs == nil {
		return []Ref{}
	}
	return refs
}

// NameOf is the name of a container without Docker's leading slash, or its short ID if it has none.
func NameOf(c container.Summary) string {
	if len(c.Names) == 0 {
		return ShortID(c.ID)
	}
	return strings.TrimPrefix(c.Names[0], "/")
}

// ShortID is the 12-character form of an ID, as docker ps shows it.
func ShortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
