// Package containers implements the container routes: list, details, create, edit (directly or by recreating),
// start, stop, restart, delete, logs and statistics.
package containers

import (
	"cmp"
	"fmt"
	"math"
	"net/netip"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/shellwords"
)

// Spec is the form of a container, for creating and for editing.
type Spec struct {
	Name       string   `json:"name"`
	Image      string   `json:"image"`
	Command    string   `json:"command"` // one line, split like a shell; "" is the image's command
	Ports      []Port   `json:"ports"`
	Mounts     []Mount  `json:"mounts"`
	Env        []EnvVar `json:"env"`
	Labels     []Label  `json:"labels"`
	Network    string   `json:"network"`
	Restart    Restart  `json:"restart"`
	Privileged bool     `json:"privileged"`
	// Resources are the memory and CPU limits.
	Resources Resources `json:"resources"`
	Start     bool      `json:"start,omitempty"` // creating only: start the new container
}

// Port publishes a container port; HostPort 0 lets Docker choose one, HostIP "" listens on all addresses.
type Port struct {
	HostIP        string `json:"hostIp"`
	HostPort      int    `json:"hostPort"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
}

// Mount is a volume (Source = its name) or a bind mount (Source = a path on the host).
type Mount struct {
	Type      string `json:"type"`
	Source    string `json:"source"`
	Target    string `json:"target"`
	ReadOnly  bool   `json:"readOnly"`
	Anonymous bool   `json:"anonymous,omitempty"` // view only: an anonymous volume of the container
}

// EnvVar is one environment variable.
type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Label is one label of a container: a key (case-sensitive) and a value, which may be empty. The form's labels are the
// container's own; Docker's (see reservedLabel) are not part of the form.
type Label struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// reservedLabelPrefixes are the namespaces of label keys that Docker keeps for itself (com.docker.compose.project, ...).
var reservedLabelPrefixes = []string{"com.docker.", "io.docker.", "org.dockerproject."}

// reservedLabel reports whether key is in a namespace Docker keeps for itself. The form neither shows nor sets such a
// label; a recreation keeps those of the container.
func reservedLabel(key string) bool {
	return slices.ContainsFunc(reservedLabelPrefixes, func(p string) bool { return strings.HasPrefix(key, p) })
}

// Restart is the restart policy; MaxRetries counts for on-failure only.
type Restart struct {
	Policy     string `json:"policy"`
	MaxRetries int    `json:"maxRetries"`
}

var policies = []string{"no", "always", "unless-stopped", "on-failure"}

var networkPattern = regexp.MustCompile(`^(container:)?[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// FieldError is an invalid field of a Spec; Field is a path like "ports.0.hostPort".
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Field + " " + e.Message }

func fieldErr(field, msg string, args ...any) error {
	return &FieldError{Field: field, Message: fmt.Sprintf(msg, args...)}
}

// Normalize trims the values (the keys of the labels, but not their values), fills the defaults (protocol tcp, network
// bridge, policy no) and drops MaxRetries outside on-failure. It keeps the order of the ports, mounts, environment and
// labels: the indices of a FieldError (ports.1.hostPort) name the rows of the request, which is the rows of the form.
// Where the order must not matter, compare sorted Specs.
func (s Spec) Normalize() Spec {
	s.Name, s.Image, s.Command = strings.TrimSpace(s.Name), strings.TrimSpace(s.Image), strings.TrimSpace(s.Command)
	if s.Network = strings.TrimSpace(s.Network); s.Network == "" || s.Network == "default" {
		s.Network = "bridge"
	}
	if s.Restart.Policy = strings.TrimSpace(s.Restart.Policy); s.Restart.Policy == "" {
		s.Restart.Policy = "no"
	}
	if s.Restart.Policy != "on-failure" {
		s.Restart.MaxRetries = 0
	}
	ports := make([]Port, len(s.Ports))
	for i, p := range s.Ports {
		p.HostIP, p.Protocol = strings.TrimSpace(p.HostIP), strings.ToLower(strings.TrimSpace(p.Protocol))
		if p.Protocol == "" {
			p.Protocol = "tcp"
		}
		ports[i] = p
	}
	mounts := make([]Mount, len(s.Mounts))
	for i, m := range s.Mounts {
		m.Type, m.Source, m.Target = strings.ToLower(strings.TrimSpace(m.Type)), strings.TrimSpace(m.Source), strings.TrimSpace(m.Target)
		mounts[i] = m
	}
	env := make([]EnvVar, len(s.Env))
	for i, e := range s.Env {
		env[i] = EnvVar{Name: strings.TrimSpace(e.Name), Value: e.Value}
	}
	labels := make([]Label, len(s.Labels))
	for i, l := range s.Labels {
		labels[i] = Label{Key: strings.TrimSpace(l.Key), Value: l.Value}
	}
	s.Ports, s.Mounts, s.Env, s.Labels = ports, mounts, env, labels
	return s
}

// sorted is s with the ports (by container port, protocol, host IP and host port), the mounts (by target), the
// environment (by name) and the labels (by key) in a fixed order, so that two Specs of the same container compare equal
// whatever order their rows came in. s is not changed.
func (s Spec) sorted() Spec {
	s.Ports = slices.Clone(s.Ports)
	slices.SortStableFunc(s.Ports, func(a, b Port) int {
		return cmp.Or(cmp.Compare(a.ContainerPort, b.ContainerPort), strings.Compare(a.Protocol, b.Protocol),
			strings.Compare(a.HostIP, b.HostIP), cmp.Compare(a.HostPort, b.HostPort))
	})
	s.Mounts = slices.Clone(s.Mounts)
	slices.SortStableFunc(s.Mounts, func(a, b Mount) int { return strings.Compare(a.Target, b.Target) })
	s.Env = slices.Clone(s.Env)
	slices.SortStableFunc(s.Env, func(a, b EnvVar) int { return strings.Compare(a.Name, b.Name) })
	s.Labels = slices.Clone(s.Labels)
	slices.SortStableFunc(s.Labels, func(a, b Label) int { return strings.Compare(a.Key, b.Key) })
	return s
}

// Validate checks a normalized Spec. The indices in the fields of its errors are those of the rows in s.
func (s Spec) Validate() error {
	if !dockerapi.NamePattern.MatchString(s.Name) {
		return fieldErr("name", "may only contain letters, digits and _ . - (at least 2 characters, starting with a letter or digit)")
	}
	if s.Image == "" || strings.ContainsAny(s.Image, " \t\n") {
		return fieldErr("image", "is required and must not contain spaces")
	}
	if !dockerapi.SafeRef(s.Image) {
		return fieldErr("image", "must be an image reference like nginx:1.29")
	}
	if _, err := shellwords.Split(s.Command); err != nil {
		return fieldErr("command", "%v", err)
	}
	if !networkPattern.MatchString(s.Network) {
		return fieldErr("network", "is not a network name")
	}
	if len(s.Ports) > 0 && (s.Network == "host" || s.Network == "none" || strings.HasPrefix(s.Network, "container:")) {
		return fieldErr("ports", "cannot be published with the network %s", s.Network)
	}
	published := map[string]bool{}
	for i, p := range s.Ports {
		f := fmt.Sprintf("ports.%d.", i)
		switch {
		case p.ContainerPort < 1 || p.ContainerPort > 65535:
			return fieldErr(f+"containerPort", "must be between 1 and 65535")
		case p.HostPort < 0 || p.HostPort > 65535:
			return fieldErr(f+"hostPort", "must be between 1 and 65535, or empty")
		case p.Protocol != "tcp" && p.Protocol != "udp":
			return fieldErr(f+"protocol", "must be tcp or udp")
		}
		if p.HostIP != "" {
			if _, err := netip.ParseAddr(p.HostIP); err != nil {
				return fieldErr(f+"hostIp", "must be an IP address")
			}
		}
		if p.HostPort != 0 {
			key := p.HostIP + "/" + strconv.Itoa(p.HostPort) + "/" + p.Protocol
			if published[key] {
				return fieldErr(f+"hostPort", "is used twice")
			}
			published[key] = true
		}
	}
	targets := map[string]bool{}
	for i, m := range s.Mounts {
		f := fmt.Sprintf("mounts.%d.", i)
		switch m.Type {
		case "bind":
			if !path.IsAbs(m.Source) {
				return fieldErr(f+"source", "must be an absolute path on the host")
			}
		case "volume":
			if !dockerapi.NamePattern.MatchString(m.Source) {
				return fieldErr(f+"source", "must be the name of a volume")
			}
		default:
			return fieldErr(f+"type", "must be volume or bind")
		}
		if !path.IsAbs(m.Target) || path.Clean(m.Target) != m.Target {
			return fieldErr(f+"target", "must be an absolute path in the container")
		}
		if targets[m.Target] {
			return fieldErr(f+"target", "is used twice")
		}
		targets[m.Target] = true
	}
	names := map[string]bool{}
	for i, e := range s.Env {
		f := fmt.Sprintf("env.%d.name", i)
		if e.Name == "" || strings.ContainsAny(e.Name, "= \t\n") {
			return fieldErr(f, "must not be empty or contain = or spaces")
		}
		if names[e.Name] {
			return fieldErr(f, "is used twice")
		}
		names[e.Name] = true
	}
	keys := map[string]bool{}
	for i, l := range s.Labels {
		f := fmt.Sprintf("labels.%d.key", i)
		switch {
		case l.Key == "":
			return fieldErr(f, "must not be empty")
		case reservedLabel(l.Key):
			return fieldErr(f, "is reserved for Docker")
		case keys[l.Key]:
			return fieldErr(f, "is used twice")
		}
		keys[l.Key] = true
	}
	switch r := s.Resources; {
	case r.Memory < 0:
		return fieldErr("resources.memory", "must not be negative")
	case r.Memory > maxMemory:
		return fieldErr("resources.memory", "must not be more than 1 PiB")
	case r.Memory > 0 && r.Memory < minMemory:
		return fieldErr("resources.memory", "must be at least 6 MiB, or empty for no limit")
	case math.IsNaN(r.CPUs) || math.IsInf(r.CPUs, 0):
		return fieldErr("resources.cpus", "must be a number")
	case r.CPUs < 0:
		return fieldErr("resources.cpus", "must not be negative")
	case r.CPUs > maxCPUs: // before any conversion to NanoCPUs: a float beyond int64 gives an unspecified integer
		return fieldErr("resources.cpus", "must not be more than %d", maxCPUs)
	case r.CPUs > 0 && nanoCPUs(r.CPUs) < 10_000_000:
		return fieldErr("resources.cpus", "must be at least 0.01, or empty for no limit")
	}
	if !slices.Contains(policies, s.Restart.Policy) {
		return fieldErr("restart.policy", "must be no, always, unless-stopped or on-failure")
	}
	if s.Restart.MaxRetries < 0 {
		return fieldErr("restart.maxRetries", "must not be negative")
	}
	return nil
}

// ValidateHost checks the CPU limit against the CPUs of the host (cpus, 0 if unknown: then there is nothing to check).
func (s Spec) ValidateHost(cpus int) error {
	if cpus > 0 && nanoCPUs(s.Resources.CPUs) > int64(cpus)*1_000_000_000 {
		return fieldErr("resources.cpus", "must not be more than %d, the number of CPUs of the host", cpus)
	}
	return nil
}

// SpecOf is the form of a container, with its ports, mounts, environment and labels sorted (see sorted), so that the
// answer is the same every time. img is its image (nil if it is gone): values that came from the image (ENV, CMD, labels
// with the image's value) are left out, so that recreating with another image does not freeze them. A label of the
// container with another value than the image's label of that key is the container's own and stays. Labels in Docker's
// namespaces (see reservedLabel) are not part of the form at all: CreateOptions keeps them when it recreates.
func SpecOf(c container.InspectResponse, img *image.InspectResponse) Spec {
	s := Spec{Name: strings.TrimPrefix(c.Name, "/")}
	var imgEnv, imgCmd []string
	var imgLabels map[string]string
	if img != nil && img.Config != nil {
		imgEnv, imgCmd, imgLabels = img.Config.Env, img.Config.Cmd, img.Config.Labels
	}
	if c.Config != nil {
		s.Image = c.Config.Image
		if img == nil || !slices.Equal(c.Config.Cmd, imgCmd) {
			s.Command = shellwords.Join(c.Config.Cmd)
		}
		for _, kv := range c.Config.Env {
			if slices.Contains(imgEnv, kv) {
				continue
			}
			name, value, _ := strings.Cut(kv, "=")
			s.Env = append(s.Env, EnvVar{Name: name, Value: value})
		}
		for key, value := range c.Config.Labels {
			if iv, fromImage := imgLabels[key]; reservedLabel(key) || (fromImage && iv == value) {
				continue
			}
			s.Labels = append(s.Labels, Label{Key: key, Value: value})
		}
	}
	if h := c.HostConfig; h != nil {
		s.Network = string(h.NetworkMode)
		s.Restart = Restart{Policy: string(h.RestartPolicy.Name), MaxRetries: h.RestartPolicy.MaximumRetryCount}
		s.Privileged = h.Privileged
		s.Resources = Resources{Memory: h.Memory, CPUs: cpusOf(h.NanoCPUs)}
		for port, bindings := range h.PortBindings {
			for _, b := range bindings {
				hostPort, _ := strconv.Atoi(b.HostPort) // a range (8080-8090) is no number and shows as 0; CreateOptions carries it over
				ip := ""
				if b.HostIP.IsValid() { // an explicit 0.0.0.0 or :: stays: "" is every address and could widen the exposure
					ip = b.HostIP.String()
				}
				s.Ports = append(s.Ports, Port{HostIP: ip, HostPort: hostPort, ContainerPort: int(port.Num()), Protocol: string(port.Proto())})
			}
		}
	}
	for _, m := range c.Mounts {
		switch m.Type {
		case mount.TypeBind:
			s.Mounts = append(s.Mounts, Mount{Type: "bind", Source: m.Source, Target: m.Destination, ReadOnly: !m.RW})
		case mount.TypeVolume:
			s.Mounts = append(s.Mounts, Mount{Type: "volume", Source: m.Name, Target: m.Destination, ReadOnly: !m.RW,
				Anonymous: dockerapi.IsAnonymousVolume(m.Name, nil)})
		}
	}
	return s.Normalize().sorted()
}
