package containers

import (
	"maps"
	"net/netip"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/shellwords"
	"github.com/Pukka922/edelweiss-ext-docker/internal/usage"
)

// CreateOptions builds the options to create the container of s, which must be normalized and validated (Spec.Normalize,
// Spec.Validate): CreateOptions still answers a *FieldError, not a panic, for a port or host IP it cannot use and for a
// label key in Docker's namespaces, but its messages are those of a last line of defense. With base (editing) it starts
// from base's configuration, so everything the form does not manage stays: resources other than the memory and CPU
// limit (see memorySwap for swap), devices, capabilities, healthcheck, log driver, mounts of other types (tmpfs, ...),
// Docker's own labels (com.docker.*, io.docker.*, org.dockerproject.*, such as those of Docker Compose), and the options
// of mounts and of ports:
//   - a volume or bind mount of the form that is still there with the same type, source and target keeps the options of
//     base's mount (HostConfig.Mounts), or, if base made it with -v, its entry in HostConfig.Binds as it was (propagation,
//     SELinux z/Z, nocopy, ...), with only the ro/rw flag following the form;
//   - a host port range (8080-8090), which the form shows as host port 0, is kept for the same container port, protocol
//     and host IP while the form leaves the host port 0.
//
// The form manages the labels of the container itself (Spec.Labels, see SpecOf): they replace base's, so a label the form
// no longer has is gone, unless the image sets it, in which case Docker merges it in again.
//
// Values Docker took from base's image img (CMD, ENTRYPOINT, ENV, labels, working directory, user, stop signal,
// healthcheck, exposed ports) are not frozen, and neither is the hostname Docker derived from base's ID. The returned map
// holds base's further networks, to connect after creating: not base's primary network (HostConfig.NetworkMode), which
// the form's network replaces.
func CreateOptions(s Spec, base *container.InspectResponse, img *image.InspectResponse) (client.ContainerCreateOptions, map[string]*network.EndpointSettings, error) {
	cmd, err := shellwords.Split(s.Command)
	if err != nil {
		return client.ContainerCreateOptions{}, nil, fieldErr("command", "%v", err)
	}
	cfg, host := &container.Config{}, &container.HostConfig{}
	if base != nil && base.Config != nil {
		c := *base.Config
		c.ExposedPorts = maps.Clone(c.ExposedPorts) // below, ports are added to it: not base's own map
		cfg = &c
	}
	if base != nil && base.HostConfig != nil {
		h := *base.HostConfig
		host = &h
	}
	if base != nil {
		unfreeze(cfg, base, img)
	}
	cfg.Image = s.Image
	cfg.Cmd = cmd
	cfg.Env = nil
	for _, e := range s.Env {
		cfg.Env = append(cfg.Env, e.Name+"="+e.Value)
	}
	// Docker's own labels of base stay (unfreeze has left out those of the image, which Docker merges in again), the rest
	// are the form's. A new map: the one in cfg may be base's.
	labels := map[string]string{}
	for k, v := range cfg.Labels {
		if reservedLabel(k) {
			labels[k] = v
		}
	}
	for i, l := range s.Labels {
		if reservedLabel(l.Key) { // Validate refuses it; the form must never override or forge a label of Docker's
			return client.ContainerCreateOptions{}, nil, fieldErr("labels."+strconv.Itoa(i)+".key", "is reserved for Docker")
		}
		labels[l.Key] = l.Value
	}
	cfg.Labels = nil
	if len(labels) > 0 {
		cfg.Labels = labels
	}
	cfg.Volumes = nil // anonymous volumes are taken over as mounts by name

	if cfg.ExposedPorts == nil {
		cfg.ExposedPorts = network.PortSet{}
	}
	ranges := hostRanges(base)
	host.PortBindings = network.PortMap{}
	for i, p := range s.Ports {
		if p.ContainerPort < 1 || p.ContainerPort > 65535 || p.HostPort < 0 || p.HostPort > 65535 {
			return client.ContainerCreateOptions{}, nil, fieldErr("ports", "port %d (%d/%s) is out of range", i, p.ContainerPort, p.Protocol)
		}
		port, ok := network.PortFrom(uint16(p.ContainerPort), network.IPProtocol(p.Protocol))
		if !ok {
			return client.ContainerCreateOptions{}, nil, fieldErr("ports", "port %d (%d/%s) has no protocol", i, p.ContainerPort, p.Protocol)
		}
		var ip netip.Addr
		if p.HostIP != "" {
			if ip, err = netip.ParseAddr(p.HostIP); err != nil {
				return client.ContainerCreateOptions{}, nil, fieldErr("ports", "port %d: %q is not an IP address", i, p.HostIP)
			}
		}
		var hostPort string
		if p.HostPort != 0 {
			hostPort = strconv.Itoa(p.HostPort)
		} else {
			hostPort = ranges.take(port, ip)
		}
		cfg.ExposedPorts[port] = struct{}{}
		host.PortBindings[port] = append(host.PortBindings[port], network.PortBinding{HostIP: ip, HostPort: hostPort})
	}

	host.NetworkMode = container.NetworkMode(s.Network)
	host.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyMode(s.Restart.Policy), MaximumRetryCount: s.Restart.MaxRetries}
	host.Privileged = s.Privileged
	host.Memory, host.NanoCPUs, host.MemorySwap = s.Resources.Memory, nanoCPUs(s.Resources.CPUs), 0
	if base != nil && base.HostConfig != nil {
		swap, err := memorySwap(base.HostConfig.Memory, base.HostConfig.MemorySwap, s.Resources.Memory)
		if err != nil {
			return client.ContainerCreateOptions{}, nil, err
		}
		host.MemorySwap = swap
	}
	var baseBinds []string
	var baseMounts []mount.Mount
	if base != nil && base.HostConfig != nil {
		baseBinds, baseMounts = base.HostConfig.Binds, base.HostConfig.Mounts
	}
	host.Binds, host.Mounts = buildMounts(s.Mounts, baseBinds, baseMounts)

	opts := client.ContainerCreateOptions{Name: s.Name, Config: cfg, HostConfig: host}
	extra := map[string]*network.EndpointSettings{}
	if base != nil && base.NetworkSettings != nil {
		ownMode := s.Network == "host" || s.Network == "none" || strings.HasPrefix(s.Network, "container:")
		oldPrimary := "bridge" // an empty network mode or default is the network bridge
		if base.HostConfig != nil && base.HostConfig.NetworkMode != "" && base.HostConfig.NetworkMode != "default" {
			oldPrimary = string(base.HostConfig.NetworkMode)
		}
		for name, ep := range base.NetworkSettings.Networks {
			if ep == nil {
				continue
			}
			settings := &network.EndpointSettings{IPAMConfig: ep.IPAMConfig, Aliases: userAliases(ep.Aliases, base.ID)}
			switch {
			case name == s.Network:
				opts.NetworkingConfig = &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{name: settings}}
			case name == oldPrimary:
				// the form's network replaces it: only the further networks are connected again
			case ownMode || name == "host" || name == "none":
				// these network modes leave no room for other networks
			default:
				extra[name] = settings
			}
		}
	}
	return opts, extra, nil
}

// unfreeze clears what Docker copied from base's image into base's configuration (Docker fills it in again from the
// new image) and the hostname it derived from base's ID.
func unfreeze(cfg *container.Config, base *container.InspectResponse, img *image.InspectResponse) {
	if cfg.Hostname == usage.ShortID(base.ID) {
		cfg.Hostname = ""
	}
	if img == nil || img.Config == nil {
		return
	}
	ic := img.Config
	if slices.Equal(cfg.Entrypoint, ic.Entrypoint) {
		cfg.Entrypoint = nil
	}
	if cfg.WorkingDir == ic.WorkingDir {
		cfg.WorkingDir = ""
	}
	if cfg.User == ic.User {
		cfg.User = ""
	}
	if cfg.StopSignal == ic.StopSignal {
		cfg.StopSignal = ""
	}
	if reflect.DeepEqual(cfg.Healthcheck, ic.Healthcheck) {
		cfg.Healthcheck = nil
	}
	if len(cfg.Labels) > 0 {
		labels := map[string]string{}
		for k, v := range cfg.Labels {
			if iv, ok := ic.Labels[k]; !ok || iv != v {
				labels[k] = v
			}
		}
		cfg.Labels = labels
	}
	// exposed ports: keep the container's own ones that are not published; the image's and the published ones come
	// back from the new image and the form
	kept := network.PortSet{}
	for p := range cfg.ExposedPorts {
		if _, fromImage := ic.ExposedPorts[p.String()]; fromImage {
			continue
		}
		if base.HostConfig != nil {
			if _, published := base.HostConfig.PortBindings[p]; published {
				continue
			}
		}
		kept[p] = struct{}{}
	}
	cfg.ExposedPorts = kept
}

// buildMounts turns the form's mounts into the binds and mounts of the host configuration. oldBinds and oldMounts are
// those of the container being edited. A form mount that still matches an old bind (type, source, target) is emitted as
// that bind again, with all its options and only the ro/rw flag of the form; one that matches an old mount keeps that
// mount's options; any other becomes a plain mount. Old mounts of other types (tmpfs, ...), which the form does not
// show, stay as they were. No target is in both lists.
func buildMounts(form []Mount, oldBinds []string, oldMounts []mount.Mount) (binds []string, mounts []mount.Mount) {
	var entries []bindEntry
	for _, b := range oldBinds {
		if e, ok := parseBind(b); ok {
			entries = append(entries, e)
		}
	}
	for _, m := range form {
		if i := slices.IndexFunc(entries, func(e bindEntry) bool { return e.typ == m.Type && e.source == m.Source && e.target == m.Target }); i >= 0 {
			binds = append(binds, entries[i].format(m.ReadOnly))
			continue
		}
		t := mount.TypeVolume
		if m.Type == "bind" {
			t = mount.TypeBind
		}
		nm := mount.Mount{Type: t, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly}
		for _, o := range oldMounts {
			if o.Type == t && o.Source == m.Source && o.Target == m.Target {
				nm = o
				nm.ReadOnly = m.ReadOnly
				break
			}
		}
		mounts = append(mounts, nm)
	}
	for _, o := range oldMounts {
		if o.Type != mount.TypeBind && o.Type != mount.TypeVolume {
			mounts = append(mounts, o)
		}
	}
	return binds, mounts
}

// bindEntry is an entry of HostConfig.Binds, source:target[:options] with comma-separated options. typ is the type the
// form gives the mount: bind for a path on the host, volume for a volume name.
type bindEntry struct {
	typ, source, target string
	opts                []string
}

// parseBind splits an entry of HostConfig.Binds. An entry with a target alone (an anonymous volume) has no source and is
// not parsed: the form's mount for it, by volume name, is a mount. The paths are cleaned, as Docker shows them in the
// form (-v /srv/conf/:/etc/x/ is /srv/conf on /etc/x).
func parseBind(b string) (bindEntry, bool) {
	parts := strings.SplitN(b, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return bindEntry{}, false
	}
	e := bindEntry{typ: "volume", source: parts[0], target: path.Clean(parts[1])}
	if path.IsAbs(e.source) {
		e.typ, e.source = "bind", path.Clean(e.source)
	}
	if len(parts) == 3 && parts[2] != "" {
		e.opts = strings.Split(parts[2], ",")
	}
	return e, true
}

// format is the entry again with the access mode of the form: ro or rw replaces the one it had, in place; without one it
// is added after the other options for a read-only mount (rw is Docker's default).
func (e bindEntry) format(readOnly bool) string {
	mode := "rw"
	if readOnly {
		mode = "ro"
	}
	var opts []string
	replaced := false
	for _, o := range e.opts {
		switch {
		case o != "ro" && o != "rw":
			opts = append(opts, o)
		case !replaced: // a second ro/rw is dropped
			opts = append(opts, mode)
			replaced = true
		}
	}
	if !replaced && readOnly {
		opts = append(opts, mode)
	}
	s := e.source + ":" + e.target
	if len(opts) > 0 {
		s += ":" + strings.Join(opts, ",")
	}
	return s
}

// portRanges are the host port ranges (8080-8090) among base's published ports, which the form shows as host port 0.
type portRanges network.PortMap

func hostRanges(base *container.InspectResponse) portRanges {
	r := portRanges{}
	if base == nil || base.HostConfig == nil {
		return r
	}
	for port, bindings := range base.HostConfig.PortBindings {
		for _, b := range bindings {
			if _, err := strconv.Atoi(b.HostPort); err != nil && b.HostPort != "" {
				r[port] = append(r[port], b)
			}
		}
	}
	return r
}

// take returns the host port range of port on hostIP and uses it up (two entries of the form must not get the same
// range), or "" if there is none: then Docker chooses the host port. A single host port of base that the form clears
// is not a range and is not brought back.
func (r portRanges) take(port network.Port, hostIP netip.Addr) string {
	for i, b := range r[port] {
		if b.HostIP == hostIP {
			r[port] = slices.Delete(r[port], i, i+1)
			return b.HostPort
		}
	}
	return ""
}

// userAliases drops the alias older daemons added for the container's short ID.
func userAliases(aliases []string, id string) []string {
	var out []string
	for _, a := range aliases {
		if a != usage.ShortID(id) {
			out = append(out, a)
		}
	}
	return out
}
