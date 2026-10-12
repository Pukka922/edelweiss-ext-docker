// Package networks implements the network routes: list, details with the connected containers, create, connect and
// disconnect containers, delete (not bridge, host and none).
package networks

import (
	"net/netip"
	"slices"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/httpx"
	"github.com/Pukka922/edelweiss-ext-docker/internal/usage"
	"github.com/Pukka922/edelweiss/sdk"
)

// Predefined are the networks every daemon has; they cannot be deleted.
var Predefined = []string{"bridge", "host", "none"}

var drivers = []string{"bridge", "macvlan", "ipvlan"}

// Network is a network as the list shows it.
type Network struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Driver     string `json:"driver"`
	Scope      string `json:"scope"`
	Subnet     string `json:"subnet,omitempty"`
	Gateway    string `json:"gateway,omitempty"`
	Internal   bool   `json:"internal"`
	Predefined bool   `json:"predefined"`
	Containers int    `json:"containers"`
}

// Attached is a container connected to a network.
type Attached struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	IPv4 string `json:"ipv4,omitempty"`
}

// Detail is the answer of GET /networks/:id.
type Detail struct {
	Network
	Options  map[string]string `json:"options"`
	Labels   map[string]string `json:"labels"`
	Attached []Attached        `json:"attached"`
}

// CreateRequest is the body of POST /networks.
type CreateRequest struct {
	Name     string            `json:"name"`
	Driver   string            `json:"driver"`
	Subnet   string            `json:"subnet"`
	Gateway  string            `json:"gateway"`
	Internal bool              `json:"internal"`
	Options  map[string]string `json:"options"`
}

// ConnectRequest is the body of POST /networks/:id/connect (and, without IPv4, of disconnect).
type ConnectRequest struct {
	Container string `json:"container"`
	IPv4      string `json:"ipv4"`
}

type handlers struct{ docker dockerapi.Docker }

// Register adds the network routes.
func Register(r fiber.Router, d dockerapi.Docker) {
	h := handlers{docker: d}
	r.Get("/networks", h.list)
	r.Post("/networks", h.create)
	r.Get("/networks/:id", h.get)
	r.Post("/networks/:id/connect", h.connect)
	r.Post("/networks/:id/disconnect", h.disconnect)
	r.Delete("/networks/:id", h.remove)
}

func summaryOf(n network.Network, containers int) Network {
	v := Network{ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal,
		Predefined: slices.Contains(Predefined, n.Name), Containers: containers}
	for _, cfg := range n.IPAM.Config {
		if cfg.Subnet.IsValid() {
			v.Subnet = cfg.Subnet.String()
			if cfg.Gateway.IsValid() {
				v.Gateway = cfg.Gateway.String()
			}
			break
		}
	}
	return v
}

func (h handlers) list(c fiber.Ctx) error {
	res, err := h.docker.NetworkList(c.Context(), client.NetworkListOptions{})
	if err != nil {
		return dockerapi.Error(err)
	}
	idx, err := usage.Load(c.Context(), h.docker)
	if err != nil {
		return dockerapi.Error(err)
	}
	out := make([]Network, 0, len(res.Items))
	for _, n := range res.Items {
		out = append(out, summaryOf(n.Network, len(idx.Network(n.Name))))
	}
	slices.SortFunc(out, func(a, b Network) int { return strings.Compare(a.Name, b.Name) })
	return c.JSON(out)
}

func (h handlers) get(c fiber.Ctx) error {
	res, err := h.docker.NetworkInspect(c.Context(), c.Params("id"), client.NetworkInspectOptions{})
	if err != nil {
		return dockerapi.Error(err)
	}
	n := res.Network
	d := Detail{Network: summaryOf(n.Network, len(n.Containers)), Options: orEmpty(n.Options), Labels: orEmpty(n.Labels), Attached: []Attached{}}
	for id, ep := range n.Containers {
		a := Attached{ID: id, Name: ep.Name}
		if ep.IPv4Address.IsValid() {
			a.IPv4 = ep.IPv4Address.Addr().String()
		}
		d.Attached = append(d.Attached, a)
	}
	slices.SortFunc(d.Attached, func(a, b Attached) int { return strings.Compare(a.Name, b.Name) })
	return c.JSON(d)
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

func invalid(field, msg string) error {
	return sdk.NewError(fiber.StatusBadRequest, "invalid_request", field+" "+msg).WithDetails(map[string]any{"field": field})
}

func (h handlers) create(c fiber.Ctx) error {
	var req CreateRequest
	if err := httpx.Body(c, &req); err != nil {
		return err
	}
	name := strings.TrimSpace(req.Name)
	if !dockerapi.NamePattern.MatchString(name) {
		return invalid("name", "may only contain letters, digits and _ . - (at least 2 characters, starting with a letter or digit)")
	}
	driver := strings.TrimSpace(req.Driver)
	if driver == "" {
		driver = "bridge"
	}
	if !slices.Contains(drivers, driver) {
		return invalid("driver", "must be bridge, macvlan or ipvlan")
	}
	opts := client.NetworkCreateOptions{Driver: driver, Internal: req.Internal, Options: req.Options}
	for k := range req.Options {
		if strings.TrimSpace(k) == "" {
			return invalid("options", "must not have an empty key")
		}
	}
	if s := strings.TrimSpace(req.Subnet); s != "" {
		subnet, err := netip.ParsePrefix(s)
		if err != nil || subnet != subnet.Masked() {
			return invalid("subnet", "must be a network like 172.30.0.0/24")
		}
		cfg := network.IPAMConfig{Subnet: subnet}
		if g := strings.TrimSpace(req.Gateway); g != "" {
			gw, err := netip.ParseAddr(g)
			if err != nil || !subnet.Contains(gw) {
				return invalid("gateway", "must be an address in the subnet")
			}
			cfg.Gateway = gw
		}
		opts.IPAM = &network.IPAM{Driver: "default", Config: []network.IPAMConfig{cfg}}
	} else if strings.TrimSpace(req.Gateway) != "" {
		return invalid("gateway", "needs a subnet")
	}
	res, err := h.docker.NetworkCreate(c.Context(), name, opts)
	if err != nil {
		return dockerapi.Error(err)
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"id": res.ID})
}

func (h handlers) connect(c fiber.Ctx) error {
	var req ConnectRequest
	if err := httpx.Body(c, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Container) == "" {
		return invalid("container", "is required")
	}
	opts := client.NetworkConnectOptions{Container: strings.TrimSpace(req.Container)}
	if s := strings.TrimSpace(req.IPv4); s != "" {
		ip, err := netip.ParseAddr(s)
		if err != nil || !ip.Is4() {
			return invalid("ipv4", "must be an IPv4 address")
		}
		opts.EndpointConfig = &network.EndpointSettings{IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: ip}}
	}
	if _, err := h.docker.NetworkConnect(c.Context(), c.Params("id"), opts); err != nil {
		return dockerapi.Error(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h handlers) disconnect(c fiber.Ctx) error {
	var req ConnectRequest
	if err := httpx.Body(c, &req); err != nil {
		return err
	}
	if strings.TrimSpace(req.Container) == "" {
		return invalid("container", "is required")
	}
	if _, err := h.docker.NetworkDisconnect(c.Context(), c.Params("id"), client.NetworkDisconnectOptions{Container: strings.TrimSpace(req.Container)}); err != nil {
		return dockerapi.Error(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h handlers) remove(c fiber.Ctx) error {
	res, err := h.docker.NetworkInspect(c.Context(), c.Params("id"), client.NetworkInspectOptions{})
	if err != nil {
		return dockerapi.Error(err)
	}
	if slices.Contains(Predefined, res.Network.Name) {
		return sdk.NewError(fiber.StatusConflict, "network_predefined", "the networks bridge, host and none cannot be deleted")
	}
	if len(res.Network.Containers) > 0 {
		return sdk.NewError(fiber.StatusConflict, "conflict", "the network still has connected containers")
	}
	if _, err := h.docker.NetworkRemove(c.Context(), res.Network.ID, client.NetworkRemoveOptions{}); err != nil {
		// A container connected since the inspect: Docker 29 answers 403 ("has active endpoints"), which is a conflict.
		if cerrdefs.IsPermissionDenied(err) {
			return sdk.NewError(fiber.StatusConflict, "conflict", dockerapi.Message(err))
		}
		return dockerapi.Error(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}
