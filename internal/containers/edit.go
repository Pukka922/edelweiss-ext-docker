package containers

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/httpx"
	"github.com/Pukka922/edelweiss-ext-docker/internal/shellwords"
	"github.com/Pukka922/edelweiss-ext-docker/internal/usage"
	"github.com/Pukka922/edelweiss/sdk"
)

// parkedSuffix is appended to the name of the old container while its replacement is created.
const parkedSuffix = "-edelweiss-old"

// EditResult is the answer of PUT /containers/:id. Warning is set when the new container runs but the old one could
// not be removed.
type EditResult struct {
	ID        string `json:"id"`
	Recreated bool   `json:"recreated"`
	Warning   string `json:"warning,omitempty"`
	// Warnings are Docker's warnings about the new configuration (for example limits the kernel cannot enforce).
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Service) edit(c fiber.Ctx) error {
	id, err := containerID(c)
	if err != nil {
		return err
	}
	var spec Spec
	if err := httpx.Body(c, &spec); err != nil {
		return err
	}
	spec = spec.Normalize()
	if err := spec.Validate(); err != nil {
		return specError(err)
	}
	if spec.Resources.CPUs > 0 { // only a CPU limit needs the host's CPU count
		if err := spec.ValidateHost(s.hostCPUs(c.Context())); err != nil {
			return specError(err)
		}
	}
	res, err := s.Edit(c.Context(), id, spec)
	if err != nil {
		return err
	}
	return c.JSON(res)
}

// Edit applies spec to the container id: directly when only the name, the restart policy and the limits change and no
// limit is removed, otherwise by recreating it from its current configuration (Docker cannot change the labels, ports,
// mounts, ... of a container), which also happens when the image ref of the form now names another image than the
// container runs (after a pull). A recreation that fails is rolled back.
// Whatever ctx does, the edit is carried through (or rolled back) once it has begun: a caller that gives up must not
// leave a half-done recreation.
func (s *Service) Edit(ctx context.Context, id string, spec Spec) (EditResult, error) {
	ctx = context.WithoutCancel(ctx)
	s.editMu.Lock()
	defer s.editMu.Unlock()
	res, err := s.Docker.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return EditResult{}, dockerapi.Error(err)
	}
	base := res.Container
	img := s.image(ctx, base.Image)
	have := SpecOf(base, img)
	if onlyDirectChanges(have, spec) && !limitRemoved(have, spec) && !s.imageMoved(ctx, base, spec.Image) {
		return s.editDirect(ctx, base, have, spec)
	}
	return s.recreate(ctx, base, img, spec)
}

// onlyDirectChanges reports whether want differs from have in nothing but the name, the restart policy and the limits
// (which an update can change). The commands count as equal when they split into the same words (spacing and quoting do
// not matter); one that does not split is a change. The order of the ports, mounts, variables and labels does not
// matter. The labels are not blanked below: a change of them is another change than those an update can make.
func onlyDirectChanges(have, want Spec) bool {
	a, b := have.Normalize().sorted(), want.Normalize().sorted()
	wordsA, errA := shellwords.Split(a.Command)
	wordsB, errB := shellwords.Split(b.Command)
	if errA != nil || errB != nil || !slices.Equal(wordsA, wordsB) {
		return false
	}
	for _, s := range []*Spec{&a, &b} {
		s.Name, s.Restart, s.Start, s.Command, s.Resources = "", Restart{}, false, "", Resources{}
		for i := range s.Mounts {
			s.Mounts[i].Anonymous = false
		}
	}
	return reflect.DeepEqual(a, b)
}

// limitRemoved reports whether want drops a limit have sets: Docker cannot reliably take a limit back with an update,
// so that recreates the container.
func limitRemoved(have, want Spec) bool {
	return (have.Resources.Memory > 0 && want.Resources.Memory == 0) ||
		(nanoCPUs(have.Resources.CPUs) > 0 && nanoCPUs(want.Resources.CPUs) == 0)
}

// imageMoved reports whether ref now names another local image than the one base was created from: a pull moved the tag.
// A ref that names no local image (or one that cannot be read) is not a move: there is nothing to recreate from.
func (s *Service) imageMoved(ctx context.Context, base container.InspectResponse, ref string) bool {
	img := s.image(ctx, ref)
	return img != nil && img.ID != base.Image
}

// withNote is the answer for err (as dockerapi.Error maps it) with note appended to the message; status and code stay.
func withNote(err error, note string) error {
	err = dockerapi.Error(err)
	var apiErr *sdk.APIError
	if errors.As(err, &apiErr) {
		return sdk.NewError(apiErr.Status, apiErr.Code, apiErr.Message+"; "+note).WithDetails(apiErr.Details)
	}
	return err
}

// editDirect renames the container and updates its restart policy and limits in one update. The update is worked out
// before anything changes, so a limit Docker would refuse for the swap limit changes nothing. If the update fails after
// the rename, the rename is taken back (as far as that works), and the answer says which it was.
func (s *Service) editDirect(ctx context.Context, base container.InspectResponse, have, want Spec) (EditResult, error) {
	var opts client.ContainerUpdateOptions
	if want.Restart != have.Restart {
		policy := container.RestartPolicy{Name: container.RestartPolicyMode(want.Restart.Policy), MaximumRetryCount: want.Restart.MaxRetries}
		opts.RestartPolicy = &policy
	}
	if want.Resources.Memory != have.Resources.Memory || nanoCPUs(want.Resources.CPUs) != nanoCPUs(have.Resources.CPUs) {
		var baseMemory, baseSwap int64
		if base.HostConfig != nil {
			baseMemory, baseSwap = base.HostConfig.Memory, base.HostConfig.MemorySwap
		}
		swap, err := memorySwap(baseMemory, baseSwap, want.Resources.Memory)
		if err != nil {
			return EditResult{}, specError(err)
		}
		opts.Resources = &container.Resources{Memory: want.Resources.Memory, MemorySwap: swap, NanoCPUs: nanoCPUs(want.Resources.CPUs)}
	}
	renamed := false
	if want.Name != have.Name {
		if _, err := s.Docker.ContainerRename(ctx, base.ID, client.ContainerRenameOptions{NewName: want.Name}); err != nil {
			return EditResult{}, dockerapi.Error(err)
		}
		renamed = true
	}
	var warnings []string
	if opts.RestartPolicy != nil || opts.Resources != nil {
		res, err := s.Docker.ContainerUpdate(ctx, base.ID, opts)
		if err != nil {
			if !renamed {
				return EditResult{}, dockerapi.Error(err)
			}
			if _, back := s.Docker.ContainerRename(ctx, base.ID, client.ContainerRenameOptions{NewName: have.Name}); back != nil {
				return EditResult{}, withNote(err, fmt.Sprintf("the container is now called %s and could not be renamed back to %s: %s",
					want.Name, have.Name, dockerapi.Message(back)))
			}
			return EditResult{}, withNote(err, fmt.Sprintf("the rename to %s was undone", want.Name))
		}
		warnings = res.Warnings
	}
	return EditResult{ID: base.ID, Warnings: warnings}, nil
}

// recreate replaces base by a container of spec: park the old one under another name, create the new one, connect its
// other networks, stop the old one and start the new one (if the old one ran), then remove the old one (without its
// volumes: the new one uses the anonymous ones). Until the old one is removed every step can be undone. A container
// with --rm (AutoRemove) is not recreated: Docker removes it when it stops, which would leave nothing to roll back to.
func (s *Service) recreate(ctx context.Context, base container.InspectResponse, img *image.InspectResponse, spec Spec) (EditResult, error) {
	if base.HostConfig != nil && base.HostConfig.AutoRemove {
		return EditResult{}, sdk.NewError(fiber.StatusConflict, "conflict",
			"the container was started with --rm (removed when it stops), so it cannot be recreated: stopping the old container "+
				"would remove it, and a failed recreation could not be undone; only its name can be changed here")
	}
	if err := s.requireImage(ctx, spec.Image); err != nil {
		return EditResult{}, err
	}
	opts, extra, err := CreateOptions(spec, &base, img)
	if err != nil {
		return EditResult{}, specError(err)
	}
	done := sdk.Busy()
	defer done()

	oldName := strings.TrimPrefix(base.Name, "/")
	parked := oldName + parkedSuffix
	running := base.State != nil && base.State.Running
	timeout := s.StopTimeout
	if _, err := s.Docker.ContainerRename(ctx, base.ID, client.ContainerRenameOptions{NewName: parked}); err != nil {
		return EditResult{}, dockerapi.Error(err)
	}

	// stopTried is set before the stop is asked for: a stop that failed may still have stopped the container
	created, stopTried := "", false
	undo := func(cause error) error {
		var problems []string
		newLeft, renamedBack := false, true
		if created != "" {
			if _, err := s.Docker.ContainerRemove(ctx, created, client.ContainerRemoveOptions{Force: true}); err != nil {
				newLeft = true
				problems = append(problems, "removing the new container: "+dockerapi.Message(err))
			}
		}
		if _, err := s.Docker.ContainerRename(ctx, base.ID, client.ContainerRenameOptions{NewName: oldName}); err != nil {
			renamedBack = false
			problems = append(problems, fmt.Sprintf("renaming %s back to %s: %s", parked, oldName, dockerapi.Message(err)))
		}
		if stopTried {
			if _, err := s.Docker.ContainerStart(ctx, base.ID, client.ContainerStartOptions{}); err != nil {
				problems = append(problems, "starting the old container again: "+dockerapi.Message(err))
			}
		}
		if len(problems) == 0 {
			return dockerapi.Error(cause)
		}
		where := oldName
		if !renamedBack {
			where = parked
		}
		left := ""
		if newLeft {
			left = fmt.Sprintf("; the new container %s (%s) is still there", spec.Name, usage.ShortID(created))
		}
		return sdk.NewError(fiber.StatusBadGateway, "recreate_failed",
			fmt.Sprintf("%s; restoring the old container failed: %s (the old container is called %s%s)",
				dockerapi.Message(cause), strings.Join(problems, "; "), where, left))
	}

	res, err := s.Docker.ContainerCreate(ctx, opts)
	if err != nil {
		return EditResult{}, undo(err)
	}
	created = res.ID
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if _, err := s.Docker.NetworkConnect(ctx, name, client.NetworkConnectOptions{Container: created, EndpointConfig: extra[name]}); err != nil {
			return EditResult{}, undo(err)
		}
	}
	if running {
		stopTried = true
		if _, err := s.Docker.ContainerStop(ctx, base.ID, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
			return EditResult{}, undo(err)
		}
		if _, err := s.Docker.ContainerStart(ctx, created, client.ContainerStartOptions{}); err != nil {
			return EditResult{}, undo(err)
		}
	}
	out := EditResult{ID: created, Recreated: true, Warnings: res.Warnings}
	if _, err := s.Docker.ContainerRemove(ctx, base.ID, client.ContainerRemoveOptions{}); err != nil {
		out.Warning = fmt.Sprintf("the new container runs, but the old one could not be removed (%s); remove %s by hand",
			dockerapi.Message(err), parked)
	}
	return out, nil
}
