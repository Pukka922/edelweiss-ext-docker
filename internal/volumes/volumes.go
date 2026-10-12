// Package volumes implements the volume routes: list (with the containers using each volume), create, delete.
package volumes

import (
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/volume"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/httpx"
	"github.com/Pukka922/edelweiss-ext-docker/internal/usage"
	"github.com/Pukka922/edelweiss/sdk"
)

// Volume is a volume as the UI sees it.
type Volume struct {
	Name       string            `json:"name"`
	Driver     string            `json:"driver"`
	Mountpoint string            `json:"mountpoint"`
	CreatedAt  string            `json:"createdAt"`
	Scope      string            `json:"scope"`
	Options    map[string]string `json:"options"`
	Anonymous  bool              `json:"anonymous"`
	UsedBy     []usage.Ref       `json:"usedBy"`
}

// CreateRequest is the body of POST /volumes. Name may be empty (Docker picks one); Driver defaults to local.
type CreateRequest struct {
	Name    string            `json:"name"`
	Driver  string            `json:"driver"`
	Options map[string]string `json:"options"`
}

type handlers struct{ docker dockerapi.Docker }

// Register adds the volume routes.
func Register(r fiber.Router, d dockerapi.Docker) {
	h := handlers{docker: d}
	r.Get("/volumes", h.list)
	r.Post("/volumes", h.create)
	r.Delete("/volumes/:name", h.remove)
}

func viewOf(v volume.Volume, users []usage.Ref) Volume {
	opts := v.Options
	if opts == nil {
		opts = map[string]string{}
	}
	return Volume{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, CreatedAt: v.CreatedAt, Scope: v.Scope,
		Options: opts, Anonymous: dockerapi.IsAnonymousVolume(v.Name, v.Labels), UsedBy: users}
}

func (h handlers) list(c fiber.Ctx) error {
	res, err := h.docker.VolumeList(c.Context(), client.VolumeListOptions{})
	if err != nil {
		return dockerapi.Error(err)
	}
	idx, err := usage.Load(c.Context(), h.docker)
	if err != nil {
		return dockerapi.Error(err)
	}
	out := make([]Volume, 0, len(res.Items))
	for _, v := range res.Items {
		out = append(out, viewOf(v, idx.Volume(v.Name)))
	}
	slices.SortFunc(out, func(a, b Volume) int { return strings.Compare(a.Name, b.Name) })
	return c.JSON(out)
}

func invalid(field, msg string) error {
	return sdk.NewError(fiber.StatusBadRequest, "invalid_request", field+" "+msg).WithDetails(map[string]any{"field": field})
}

func (h handlers) create(c fiber.Ctx) error {
	var req CreateRequest
	if err := httpx.Body(c, &req); err != nil {
		return err
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name != "" && !dockerapi.NamePattern.MatchString(req.Name) {
		return invalid("name", "may only contain letters, digits and _ . - (at least 2 characters, starting with a letter or digit)")
	}
	if req.Driver = strings.TrimSpace(req.Driver); req.Driver == "" {
		req.Driver = "local"
	}
	for k := range req.Options {
		if strings.TrimSpace(k) == "" {
			return invalid("options", "must not have an empty key")
		}
	}
	res, err := h.docker.VolumeCreate(c.Context(), client.VolumeCreateOptions{Name: req.Name, Driver: req.Driver, DriverOpts: req.Options})
	if err != nil {
		return dockerapi.Error(err)
	}
	return c.Status(fiber.StatusCreated).JSON(viewOf(res.Volume, []usage.Ref{}))
}

func (h handlers) remove(c fiber.Ctx) error {
	name := c.Params("name")
	idx, err := usage.Load(c.Context(), h.docker)
	if err != nil {
		return dockerapi.Error(err)
	}
	if users := idx.Volume(name); len(users) > 0 {
		return sdk.NewError(fiber.StatusConflict, "volume_in_use", "the volume is used by containers").
			WithDetails(map[string]any{"containers": users})
	}
	if _, err := h.docker.VolumeRemove(c.Context(), name, client.VolumeRemoveOptions{}); err != nil {
		return dockerapi.Error(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
