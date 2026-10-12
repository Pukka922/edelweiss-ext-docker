// Package images implements the image routes: list with the containers using each image, presence check, delete,
// prune of unused images, and pulls with progress.
package images

import (
	"cmp"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/httpx"
	"github.com/Pukka922/edelweiss-ext-docker/internal/registries"
	"github.com/Pukka922/edelweiss-ext-docker/internal/usage"
	"github.com/Pukka922/edelweiss/sdk"
)

// Image is an image as the UI sees it.
type Image struct {
	ID      string      `json:"id"`
	Tags    []string    `json:"tags"`
	Size    int64       `json:"size"`
	Created int64       `json:"created"`
	UsedBy  []usage.Ref `json:"usedBy"`
}

// PullRequest is the body of POST /pulls: a complete Ref, or Registry (an entry's ID, "" for Docker Hub), Image and Tag.
type PullRequest struct {
	Ref      string `json:"ref"`
	Registry string `json:"registry"`
	Image    string `json:"image"`
	Tag      string `json:"tag"`
}

type handlers struct {
	docker dockerapi.Docker
	pulls  *Pulls
	store  *registries.Store
}

// minInterval keeps the progress events of a pull at four per second at most.
const minInterval = 250 * time.Millisecond

// Register adds the image and pull routes.
func Register(r fiber.Router, d dockerapi.Docker, p *Pulls, s *registries.Store) {
	h := handlers{docker: d, pulls: p, store: s}
	r.Get("/images", h.list)
	r.Get("/images/present", h.present)
	r.Post("/images/prune", h.prune)
	r.Delete("/images/:id", h.remove)
	r.Post("/pulls", h.startPull)
	r.Get("/pulls/:id/events", h.preparePull, sdk.SSE(h.pullEvents))
}

func (h handlers) list(c fiber.Ctx) error {
	res, err := h.docker.ImageList(c.Context(), client.ImageListOptions{})
	if err != nil {
		return dockerapi.Error(err)
	}
	idx, err := usage.Load(c.Context(), h.docker)
	if err != nil {
		return dockerapi.Error(err)
	}
	out := make([]Image, 0, len(res.Items))
	for _, im := range res.Items {
		tags := []string{}
		for _, t := range im.RepoTags {
			if t != "<none>:<none>" {
				tags = append(tags, t)
			}
		}
		out = append(out, Image{ID: im.ID, Tags: tags, Size: im.Size, Created: im.Created, UsedBy: idx.Image(im.ID)})
	}
	slices.SortFunc(out, func(a, b Image) int { return cmp.Compare(b.Created, a.Created) })
	return c.JSON(out)
}

func (h handlers) present(c fiber.Ctx) error {
	ref := strings.TrimSpace(c.Query("ref"))
	if ref == "" {
		return invalid("ref", "is required")
	}
	if !dockerapi.SafeRef(ref) {
		return invalid("ref", "must be an image reference like nginx:1.29")
	}
	_, err := h.docker.ImageInspect(c.Context(), ref)
	switch {
	case err == nil:
		return c.JSON(fiber.Map{"present": true})
	case cerrdefs.IsNotFound(err):
		return c.JSON(fiber.Map{"present": false})
	default:
		return dockerapi.Error(err)
	}
}

func invalid(field, msg string) error {
	return sdk.NewError(fiber.StatusBadRequest, "invalid_request", field+" "+msg).WithDetails(map[string]any{"field": field})
}

// imageID is the canonical ID of an image as the daemon lists it.
var imageID = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func (h handlers) remove(c fiber.Ctx) error {
	// Fiber does not percent-decode params; a client that encodes the colon of sha256:… sends sha256%3A…
	id, err := url.PathUnescape(c.Params("id"))
	// Only the canonical ID: the check for containers below looks an image up by its ID, so a tag or a short ID would
	// bypass it (and ImageRemove forces), and other values reach the daemon's URL path (see dockerapi.SafeRef).
	if err != nil || !imageID.MatchString(id) {
		return invalid("id", "must be the full image ID (sha256: and 64 hexadecimal characters)")
	}
	idx, err := usage.Load(c.Context(), h.docker)
	if err != nil {
		return dockerapi.Error(err)
	}
	if users := idx.Image(id); len(users) > 0 {
		return sdk.NewError(fiber.StatusConflict, "image_in_use", "the image is used by containers").
			WithDetails(map[string]any{"containers": users})
	}
	// force: an image with several tags is deleted with all of them; no container uses it (checked above)
	if _, err := h.docker.ImageRemove(c.Context(), id, client.ImageRemoveOptions{Force: true, PruneChildren: true}); err != nil {
		return dockerapi.Error(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// prune removes all unused images. Docker's report names every object it deleted: besides the images their layers
// (classic store) or their manifests, configs and layers (containerd store). The count is that of the images among
// them, by the IDs of the list before the prune; the space is Docker's.
func (h handlers) prune(c fiber.Ctx) error {
	before, err := h.docker.ImageList(c.Context(), client.ImageListOptions{})
	if err != nil {
		return dockerapi.Error(err)
	}
	listed := make(map[string]bool, len(before.Items))
	for _, im := range before.Items {
		listed[im.ID] = true
	}
	res, err := h.docker.ImagePrune(c.Context(), client.ImagePruneOptions{Filters: make(client.Filters).Add("dangling", "false")})
	if err != nil {
		return dockerapi.Error(err)
	}
	deleted := map[string]bool{}
	for _, d := range res.Report.ImagesDeleted {
		if listed[d.Deleted] {
			deleted[d.Deleted] = true
		}
	}
	return c.JSON(fiber.Map{"deleted": len(deleted), "spaceReclaimed": res.Report.SpaceReclaimed})
}

func (h handlers) startPull(c fiber.Ctx) error {
	var req PullRequest
	if err := httpx.Body(c, &req); err != nil {
		return err
	}
	ref := strings.TrimSpace(req.Ref)
	if ref == "" {
		host := ""
		if req.Registry != "" {
			r, err := h.store.Find(req.Registry)
			if err != nil {
				return registries.Error(err)
			}
			host = r.Address
		}
		var err error
		if ref, err = Reference(host, req.Image, req.Tag); err != nil {
			return sdk.NewError(fiber.StatusBadRequest, "invalid_request", err.Error()).WithDetails(map[string]any{"field": "image"})
		}
	} else if strings.ContainsAny(ref, " \t\n") {
		return sdk.NewError(fiber.StatusBadRequest, "invalid_request", "ref must not contain spaces").WithDetails(map[string]any{"field": "ref"})
	}
	p, err := h.pulls.Start(ref)
	if err != nil {
		return registries.Error(err)
	}
	return c.Status(fiber.StatusAccepted).JSON(fiber.Map{"id": p.ID(), "ref": ref})
}

const pullKey = "pull"

func (h handlers) preparePull(c fiber.Ctx) error {
	p, ok := h.pulls.Get(c.Params("id"))
	if !ok {
		return sdk.NewError(fiber.StatusNotFound, "not_found", "no such pull")
	}
	c.Locals(pullKey, p)
	return c.Next()
}

// pullEvents streams the progress of a pull: a progress event at once and on changes (four per second at most), then
// done or error. A browser that leaves only ends its stream, not the pull.
func (h handlers) pullEvents(c fiber.Ctx, s *sse.Stream) error {
	p := c.Locals(pullKey).(*Pull) // the only use of c, at the start (see sdk.SSE)
	for {
		snap, changed := p.Watch()
		if err := s.Event(sse.Event{Name: "progress", Data: snap}); err != nil {
			return nil
		}
		if snap.Done {
			if snap.Error != "" {
				_ = s.Event(sse.Event{Name: "error", Data: fiber.Map{"message": snap.Error}})
			} else {
				_ = s.Event(sse.Event{Name: "done", Data: fiber.Map{"ref": snap.Ref}})
			}
			return nil
		}
		select {
		case <-s.Done():
			return nil
		case <-changed:
		}
		select {
		case <-s.Done():
			return nil
		case <-time.After(minInterval):
		}
	}
}
