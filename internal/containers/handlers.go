package containers

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/httpx"
	"github.com/Pukka922/edelweiss-ext-docker/internal/shellwords"
	"github.com/Pukka922/edelweiss-ext-docker/internal/usage"
	"github.com/Pukka922/edelweiss/sdk"
)

// Service holds what the container routes work with.
type Service struct {
	Docker dockerapi.Docker
	// StopTimeout is how many seconds a container may take to stop (stop, delete, recreate).
	StopTimeout int
	// StatsInterval is the distance of two statistics samples.
	StatsInterval time.Duration

	editMu sync.Mutex // one edit at a time: two recreations of one container must not interleave
}

// New returns the service with the defaults: 10 s to stop, a statistics sample every 2 s.
func New(d dockerapi.Docker) *Service {
	return &Service{Docker: d, StopTimeout: 10, StatsInterval: 2 * time.Second}
}

// Register adds the container routes.
func Register(r fiber.Router, s *Service) {
	r.Get("/containers", s.list)
	r.Post("/containers", s.create)
	r.Get("/containers/:id", s.get)
	r.Put("/containers/:id", s.edit)
	r.Delete("/containers/:id", s.remove)
	r.Post("/containers/:id/start", s.start)
	r.Post("/containers/:id/stop", s.stop)
	r.Post("/containers/:id/restart", s.restart)
	r.Get("/containers/:id/logs", s.prepareStream(logsKey), sdk.SSE(s.logs))
	r.Get("/containers/:id/stats", s.prepareStream(statsKey), sdk.SSE(s.stats))
}

// containerID is the :id of the route, or dockerapi.InvalidID (400 invalid_request, field "id") for anything but a
// container name or ID (dockerapi.SafeID); the routes call it before they talk to Docker. The result is a copy: Fiber
// reuses the buffer behind c.Params, and some routes keep the id beyond the handler (streams).
func containerID(c fiber.Ctx) (string, error) {
	id := c.Params("id")
	if !dockerapi.SafeID(id) {
		return "", dockerapi.InvalidID()
	}
	return strings.Clone(id), nil
}

// PublishedPort is a port of a container; HostPort 0 means not published.
type PublishedPort struct {
	HostIP        string `json:"hostIp,omitempty"`
	HostPort      int    `json:"hostPort,omitempty"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

// Summary is a container in the list.
type Summary struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Image    string          `json:"image"`
	ImageID  string          `json:"imageId"`
	State    string          `json:"state"`
	Status   string          `json:"status"`
	Created  int64           `json:"created"`
	Ports    []PublishedPort `json:"ports"`
	Networks []string        `json:"networks"`
}

// State is the state of a container.
type State struct {
	Status     string `json:"status"`
	Running    bool   `json:"running"`
	ExitCode   int    `json:"exitCode"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
	Error      string `json:"error,omitempty"`
	Health     string `json:"health,omitempty"`
}

// Endpoint is a network the container is connected to.
type Endpoint struct {
	Name    string   `json:"name"`
	IPv4    string   `json:"ipv4,omitempty"`
	Gateway string   `json:"gateway,omitempty"`
	Aliases []string `json:"aliases"`
}

// Detail is a container as its overview shows it.
type Detail struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Image    string          `json:"image"`
	ImageID  string          `json:"imageId"`
	Created  string          `json:"created"`
	State    State           `json:"state"`
	Command  string          `json:"command"`
	Ports    []PublishedPort `json:"ports"`
	Mounts   []Mount         `json:"mounts"`
	Networks []Endpoint      `json:"networks"`
	Env      []string        `json:"env"`
	Restart  Restart         `json:"restart"`
	Tty      bool            `json:"tty"`
}

// Info is the answer of GET /containers/:id: the details and the form.
type Info struct {
	Container Detail `json:"container"`
	Spec      Spec   `json:"spec"`
}

// CreateResult is the answer of POST /containers. Warning is set when the container was created but did not start.
type CreateResult struct {
	ID       string   `json:"id"`
	Warnings []string `json:"warnings,omitempty"`
	Warning  string   `json:"warning,omitempty"`
}

func sortPorts(ports []PublishedPort) {
	slices.SortFunc(ports, func(a, b PublishedPort) int {
		return cmp.Or(cmp.Compare(a.ContainerPort, b.ContainerPort), strings.Compare(a.Protocol, b.Protocol), cmp.Compare(a.HostPort, b.HostPort))
	})
}

func portsOf(list []container.PortSummary) []PublishedPort {
	out := []PublishedPort{}
	seen := map[PublishedPort]bool{}
	for _, p := range list {
		v := PublishedPort{ContainerPort: int(p.PrivatePort), HostPort: int(p.PublicPort), Protocol: p.Type}
		if p.IP.IsValid() && !p.IP.IsUnspecified() {
			v.HostIP = p.IP.String()
		}
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sortPorts(out)
	return out
}

func (s *Service) list(c fiber.Ctx) error {
	res, err := s.Docker.ContainerList(c.Context(), client.ContainerListOptions{All: true})
	if err != nil {
		return dockerapi.Error(err)
	}
	out := make([]Summary, 0, len(res.Items))
	for _, ct := range res.Items {
		v := Summary{ID: ct.ID, Name: usage.NameOf(ct), Image: ct.Image, ImageID: ct.ImageID, State: string(ct.State),
			Status: ct.Status, Created: ct.Created, Ports: portsOf(ct.Ports), Networks: []string{}}
		if ct.NetworkSettings != nil {
			for name := range ct.NetworkSettings.Networks {
				v.Networks = append(v.Networks, name)
			}
			slices.Sort(v.Networks)
		}
		out = append(out, v)
	}
	slices.SortFunc(out, func(a, b Summary) int { return strings.Compare(a.Name, b.Name) })
	return c.JSON(out)
}

func detailOf(c container.InspectResponse) Detail {
	d := Detail{ID: c.ID, Name: strings.TrimPrefix(c.Name, "/"), ImageID: c.Image, Created: c.Created,
		Ports: []PublishedPort{}, Mounts: []Mount{}, Networks: []Endpoint{}, Env: []string{}}
	if c.Path != "" {
		d.Command = shellwords.Join(append([]string{c.Path}, c.Args...))
	}
	if st := c.State; st != nil {
		d.State = State{Status: string(st.Status), Running: st.Running, ExitCode: st.ExitCode, StartedAt: st.StartedAt,
			FinishedAt: st.FinishedAt, Error: st.Error}
		if st.Health != nil {
			d.State.Health = string(st.Health.Status)
		}
	}
	if c.Config != nil {
		d.Image, d.Tty = c.Config.Image, c.Config.Tty
		d.Env = append(d.Env, c.Config.Env...)
	}
	if c.HostConfig != nil {
		d.Restart = Restart{Policy: string(c.HostConfig.RestartPolicy.Name), MaxRetries: c.HostConfig.RestartPolicy.MaximumRetryCount}
		if d.Restart.Policy == "" {
			d.Restart.Policy = "no"
		}
	}
	for _, m := range c.Mounts {
		d.Mounts = append(d.Mounts, Mount{Type: string(m.Type), Source: cmp.Or(m.Name, m.Source), Target: m.Destination, ReadOnly: !m.RW,
			Anonymous: m.Type == "volume" && dockerapi.IsAnonymousVolume(m.Name, nil)})
	}
	if ns := c.NetworkSettings; ns != nil {
		for port, bindings := range ns.Ports {
			if len(bindings) == 0 {
				d.Ports = append(d.Ports, PublishedPort{ContainerPort: int(port.Num()), Protocol: string(port.Proto())})
			}
			for _, b := range bindings {
				hp, _ := strconv.Atoi(b.HostPort)
				v := PublishedPort{HostPort: hp, ContainerPort: int(port.Num()), Protocol: string(port.Proto())}
				if b.HostIP.IsValid() && !b.HostIP.IsUnspecified() {
					v.HostIP = b.HostIP.String()
				}
				if !slices.Contains(d.Ports, v) {
					d.Ports = append(d.Ports, v)
				}
			}
		}
		sortPorts(d.Ports)
		for name, ep := range ns.Networks {
			e := Endpoint{Name: name, Aliases: []string{}}
			if ep != nil {
				if ep.IPAddress.IsValid() {
					e.IPv4 = ep.IPAddress.String()
				}
				if ep.Gateway.IsValid() {
					e.Gateway = ep.Gateway.String()
				}
				e.Aliases = append(e.Aliases, userAliases(ep.Aliases, c.ID)...)
			}
			d.Networks = append(d.Networks, e)
		}
		slices.SortFunc(d.Networks, func(a, b Endpoint) int { return strings.Compare(a.Name, b.Name) })
	}
	return d
}

// image returns the image ref (an ID or a name), or nil if it is gone or cannot be read.
func (s *Service) image(ctx context.Context, ref string) *image.InspectResponse {
	res, err := s.Docker.ImageInspect(ctx, ref)
	if err != nil {
		return nil
	}
	return &res.InspectResponse
}

func (s *Service) get(c fiber.Ctx) error {
	id, err := containerID(c)
	if err != nil {
		return err
	}
	res, err := s.Docker.ContainerInspect(c.Context(), id, client.ContainerInspectOptions{})
	if err != nil {
		return dockerapi.Error(err)
	}
	return c.JSON(Info{Container: detailOf(res.Container), Spec: SpecOf(res.Container, s.image(c.Context(), res.Container.Image))})
}

func specError(err error) error {
	var fe *FieldError
	if errors.As(err, &fe) {
		return sdk.NewError(fiber.StatusBadRequest, "invalid_container_spec", fe.Error()).WithDetails(map[string]any{"field": fe.Field})
	}
	return err
}

func imageNotFound(ref string) error {
	return sdk.NewError(fiber.StatusNotFound, "image_not_found", fmt.Sprintf("the image %s is not available locally; pull it first", ref)).
		WithDetails(map[string]any{"image": ref})
}

// requireImage fails with image_not_found unless ref is available locally: creating a container does not pull.
func (s *Service) requireImage(ctx context.Context, ref string) error {
	_, err := s.Docker.ImageInspect(ctx, ref)
	switch {
	case err == nil:
		return nil
	case cerrdefs.IsNotFound(err):
		return imageNotFound(ref)
	default:
		return dockerapi.Error(err)
	}
}

// hostCPUs is the number of CPUs of the Docker host, 0 if Docker does not say.
func (s *Service) hostCPUs(ctx context.Context) int {
	res, err := s.Docker.Info(ctx, client.InfoOptions{})
	if err != nil {
		return 0
	}
	return res.Info.NCPU
}

func (s *Service) create(c fiber.Ctx) error {
	spec := Spec{Start: true} // an omitted "start" means true
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
	ctx := c.Context()
	if err := s.requireImage(ctx, spec.Image); err != nil {
		return err
	}
	opts, _, err := CreateOptions(spec, nil, nil)
	if err != nil {
		return specError(err)
	}
	res, err := s.Docker.ContainerCreate(ctx, opts)
	if err != nil {
		return dockerapi.Error(err)
	}
	out := CreateResult{ID: res.ID, Warnings: res.Warnings}
	if spec.Start {
		if _, err := s.Docker.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
			out.Warning = "the container was created but could not be started: " + dockerapi.Message(err)
		}
	}
	return c.Status(fiber.StatusCreated).JSON(out)
}

func (s *Service) start(c fiber.Ctx) error {
	id, err := containerID(c)
	if err != nil {
		return err
	}
	if _, err := s.Docker.ContainerStart(c.Context(), id, client.ContainerStartOptions{}); err != nil {
		return dockerapi.Error(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Service) stop(c fiber.Ctx) error {
	id, err := containerID(c)
	if err != nil {
		return err
	}
	timeout := s.StopTimeout
	if _, err := s.Docker.ContainerStop(c.Context(), id, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
		return dockerapi.Error(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (s *Service) restart(c fiber.Ctx) error {
	id, err := containerID(c)
	if err != nil {
		return err
	}
	timeout := s.StopTimeout
	if _, err := s.Docker.ContainerRestart(c.Context(), id, client.ContainerRestartOptions{Timeout: &timeout}); err != nil {
		return dockerapi.Error(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// remove deletes a container; a running one is stopped first (with the stop timeout, not killed at once). With
// volumes=1 its anonymous volumes go too: Docker removes those it created for the container, and remove those that a
// recreated container took over from the old one (it mounts them by name, so Docker leaves them behind).
func (s *Service) remove(c fiber.Ctx) error {
	name, err := containerID(c)
	if err != nil {
		return err
	}
	ctx := c.Context()
	res, err := s.Docker.ContainerInspect(ctx, name, client.ContainerInspectOptions{})
	if err != nil {
		return dockerapi.Error(err)
	}
	id := res.Container.ID
	if st := res.Container.State; st != nil && st.Running {
		timeout := s.StopTimeout
		if _, err := s.Docker.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeout}); err != nil {
			return dockerapi.Error(err)
		}
	}
	opts := client.ContainerRemoveOptions{Force: true, RemoveVolumes: c.Query("volumes") == "1"}
	if _, err := s.Docker.ContainerRemove(ctx, id, opts); err != nil {
		return dockerapi.Error(err)
	}
	if opts.RemoveVolumes {
		if err := s.removeAnonymousVolumes(ctx, res.Container.Mounts); err != nil {
			return err
		}
	}
	return c.SendStatus(fiber.StatusNoContent)
}

// removeAnonymousVolumes removes the anonymous volumes among the mounts of a removed container. One that is gone already
// (Docker removed it with the container) or still in use by another container is skipped.
func (s *Service) removeAnonymousVolumes(ctx context.Context, mounts []container.MountPoint) error {
	var first error
	var left []string
	for _, m := range mounts {
		if m.Type != mount.TypeVolume || !dockerapi.IsAnonymousVolume(m.Name, nil) {
			continue
		}
		_, err := s.Docker.VolumeRemove(ctx, m.Name, client.VolumeRemoveOptions{})
		if err == nil || cerrdefs.IsNotFound(err) || cerrdefs.IsConflict(err) {
			continue
		}
		if first == nil {
			first = err
		}
		left = append(left, m.Name)
	}
	if first != nil {
		return withNote(first, "the container was removed, but not its anonymous volumes "+strings.Join(left, ", "))
	}
	return nil
}
