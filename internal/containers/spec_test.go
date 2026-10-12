package containers

import (
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func validSpec() Spec {
	return Spec{
		Name: "web", Image: "nginx:1.29",
		Ports:   []Port{{ContainerPort: 80, HostPort: 8080}},
		Mounts:  []Mount{{Type: "volume", Source: "web-data", Target: "/usr/share/nginx/html"}, {Type: "bind", Source: "/srv/conf", Target: "/etc/nginx/conf.d", ReadOnly: true}},
		Env:     []EnvVar{{Name: "TZ", Value: "Europe/Berlin"}},
		Restart: Restart{Policy: "unless-stopped"},
	}.Normalize()
}

func TestNormalize(t *testing.T) {
	s := Spec{Name: " web ", Image: " nginx ", Ports: []Port{{ContainerPort: 80, Protocol: "TCP"}}, Restart: Restart{MaxRetries: 3}}.Normalize()
	if s.Name != "web" || s.Image != "nginx" || s.Network != "bridge" || s.Restart.Policy != "no" || s.Restart.MaxRetries != 0 ||
		s.Ports[0].Protocol != "tcp" || s.Mounts == nil || s.Env == nil || s.Labels == nil {
		t.Fatalf("normalized %+v", s)
	}
}

// The keys of the labels are trimmed like the names of the variables, the values are not; the case of a key stays
// (Docker's labels are case-sensitive).
func TestNormalizeTrimsTheKeysOfTheLabelsNotTheValues(t *testing.T) {
	s := Spec{Labels: []Label{{Key: "  Com.Example.App \t", Value: "  web "}, {Key: " ", Value: " "}}}.Normalize()
	if s.Labels[0] != (Label{Key: "Com.Example.App", Value: "  web "}) || s.Labels[1] != (Label{Key: "", Value: " "}) {
		t.Fatalf("labels %q", s.Labels)
	}
}

// The rows stay in the order of the request: the indices of a FieldError are the rows of the form (a new blank row would
// otherwise sort first and make a valid row look invalid).
func TestNormalizeKeepsTheOrderOfTheRows(t *testing.T) {
	in := Spec{
		Ports:  []Port{{ContainerPort: 90}, {ContainerPort: 80}},
		Mounts: []Mount{{Type: "volume", Source: "b", Target: "/b"}, {Type: "volume", Source: "a", Target: "/a"}},
		Env:    []EnvVar{{Name: "B"}, {Name: "A"}},
		Labels: []Label{{Key: "b"}, {Key: "a"}},
	}
	s := in.Normalize()
	if s.Ports[0].ContainerPort != 90 || s.Mounts[0].Target != "/b" || s.Env[0].Name != "B" || s.Labels[0].Key != "b" {
		t.Fatalf("normalized %+v: the rows were reordered", s)
	}
}

// Validate names the row of the request, wherever a sort would have put it: an invalid or duplicate row at the end stays
// the last one.
func TestValidateNamesTheRowOfTheRequest(t *testing.T) {
	cases := map[string]func(*Spec){
		"ports.1.containerPort": func(s *Spec) { s.Ports = []Port{{ContainerPort: 80, HostPort: 8080}, {ContainerPort: 0}} },
		"ports.2.hostPort": func(s *Spec) {
			s.Ports = []Port{{ContainerPort: 90, HostPort: 9090}, {ContainerPort: 80, HostPort: 8080}, {ContainerPort: 70, HostPort: 8080}}
		},
		"mounts.1.source": func(s *Spec) {
			s.Mounts = []Mount{{Type: "volume", Source: "web-data", Target: "/z"}, {Type: "bind", Source: "relative", Target: "/a"}}
		},
		"mounts.2.target": func(s *Spec) {
			s.Mounts = []Mount{{Type: "volume", Source: "v1", Target: "/b"}, {Type: "volume", Source: "v2", Target: "/a"}, {Type: "volume", Source: "v3", Target: "/b"}}
		},
		"env.1.name":   func(s *Spec) { s.Env = []EnvVar{{Name: "B"}, {Name: ""}} },
		"env.2.name":   func(s *Spec) { s.Env = []EnvVar{{Name: "Z"}, {Name: "A"}, {Name: "Z"}} },
		"labels.1.key": func(s *Spec) { s.Labels = []Label{{Key: "b"}, {Key: ""}} },
		"labels.2.key": func(s *Spec) { s.Labels = []Label{{Key: "z"}, {Key: "a"}, {Key: "z"}} },
	}
	for field, mutate := range cases {
		s := Spec{Name: "web", Image: "nginx"}
		mutate(&s)
		var fe *FieldError
		if err := s.Normalize().Validate(); !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("%s: Validate() = %v", field, err)
		}
	}
}

// sorted puts the rows in a fixed order and leaves its receiver (and the caller's slices) alone.
func TestSortedOrdersTheRowsWithoutChangingTheOriginal(t *testing.T) {
	in := Spec{
		Ports:  []Port{{ContainerPort: 90, Protocol: "tcp"}, {ContainerPort: 80, Protocol: "udp"}, {ContainerPort: 80, Protocol: "tcp"}},
		Mounts: []Mount{{Target: "/b"}, {Target: "/a"}},
		Env:    []EnvVar{{Name: "B"}, {Name: "A"}},
		Labels: []Label{{Key: "b"}, {Key: "A"}, {Key: "a"}},
	}
	got := in.sorted()
	if got.Ports[0] != (Port{ContainerPort: 80, Protocol: "tcp"}) || got.Ports[1].Protocol != "udp" || got.Ports[2].ContainerPort != 90 ||
		got.Mounts[0].Target != "/a" || got.Env[0].Name != "A" {
		t.Fatalf("sorted %+v", got)
	}
	if keys := []string{got.Labels[0].Key, got.Labels[1].Key, got.Labels[2].Key}; !slices.Equal(keys, []string{"A", "a", "b"}) {
		t.Fatalf("sorted labels %q, want them by key (byte order, the case counts)", keys)
	}
	if in.Ports[0].ContainerPort != 90 || in.Mounts[0].Target != "/b" || in.Env[0].Name != "B" || in.Labels[0].Key != "b" {
		t.Fatalf("the original was reordered: %+v", in)
	}
}

// A label needs a key that is not empty (after trimming), used once and not in a namespace Docker keeps for itself;
// its value may be empty. The keys keep their case, so App and app are two labels.
func TestValidateLabels(t *testing.T) {
	cases := []struct {
		name    string
		labels  []Label
		field   string // "": valid
		message string
	}{
		{"none", nil, "", ""},
		{"an empty value", []Label{{Key: "empty", Value: ""}}, "", ""},
		{"a value with blanks", []Label{{Key: "app", Value: "  web  "}}, "", ""},
		{"the case of the keys counts", []Label{{Key: "App", Value: "1"}, {Key: "app", Value: "2"}}, "", ""},
		{"a reverse DNS key", []Label{{Key: "com.example.app.version", Value: "1.2"}}, "", ""},
		{"a key that only looks reserved", []Label{{Key: "com.dockerfoo"}, {Key: "com.docker"}, {Key: "docker.com.x"}, {Key: "org.docker.x"}}, "", ""},
		{"an empty key", []Label{{Key: "ok"}, {Key: ""}}, "labels.1.key", "must not be empty"},
		{"a key of blanks", []Label{{Key: " \t"}}, "labels.0.key", "must not be empty"},
		{"a key twice", []Label{{Key: "app"}, {Key: "tier"}, {Key: "app"}}, "labels.2.key", "is used twice"},
		{"a key twice after trimming", []Label{{Key: "app"}, {Key: "  app "}}, "labels.1.key", "is used twice"},
		{"com.docker.", []Label{{Key: "com.docker.compose.project", Value: "x"}}, "labels.0.key", "is reserved for Docker"},
		{"io.docker.", []Label{{Key: "app"}, {Key: "io.docker.anything"}}, "labels.1.key", "is reserved for Docker"},
		{"org.dockerproject.", []Label{{Key: "org.dockerproject.x"}}, "labels.0.key", "is reserved for Docker"},
		{"a reserved key after trimming", []Label{{Key: " com.docker.x"}}, "labels.0.key", "is reserved for Docker"},
	}
	for _, tc := range cases {
		s := validSpec()
		s.Labels = tc.labels
		err := s.Normalize().Validate()
		var fe *FieldError
		switch {
		case tc.field == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.field != "" && (!errors.As(err, &fe) || fe.Field != tc.field || !strings.Contains(fe.Message, tc.message)):
			t.Errorf("%s: Validate() = %v, want %s %s", tc.name, err, tc.field, tc.message)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := validSpec().Validate(); err != nil {
		t.Fatalf("valid spec: %v", err)
	}
	cases := map[string]func(*Spec){
		"name":                  func(s *Spec) { s.Name = "my web" },
		"image":                 func(s *Spec) { s.Image = "" },
		"command":               func(s *Spec) { s.Command = "sh -c 'unterminated" },
		"ports.0.containerPort": func(s *Spec) { s.Ports[0].ContainerPort = 0 },
		"ports.0.hostPort":      func(s *Spec) { s.Ports[0].HostPort = 70000 },
		"ports.0.protocol":      func(s *Spec) { s.Ports[0].Protocol = "sctp" },
		"ports.0.hostIp":        func(s *Spec) { s.Ports[0].HostIP = "localhost" },
		"ports.1.hostPort":      func(s *Spec) { s.Ports = append(s.Ports, Port{ContainerPort: 81, HostPort: 8080, Protocol: "tcp"}) },
		"ports":                 func(s *Spec) { s.Network = "host" },
		"mounts.0.source":       func(s *Spec) { s.Mounts[0].Source = "a" },
		"mounts.1.source":       func(s *Spec) { s.Mounts[1].Source = "relative/path" },
		"mounts.0.target":       func(s *Spec) { s.Mounts[0].Target = "data" },
		"mounts.1.target":       func(s *Spec) { s.Mounts[1].Target = s.Mounts[0].Target },
		"mounts.0.type":         func(s *Spec) { s.Mounts[0].Type = "tmpfs" },
		"env.0.name":            func(s *Spec) { s.Env[0].Name = "A=B" },
		"env.1.name":            func(s *Spec) { s.Env = append(s.Env, EnvVar{Name: "TZ"}) },
		"restart.policy":        func(s *Spec) { s.Restart.Policy = "sometimes" },
		"restart.maxRetries":    func(s *Spec) { s.Restart = Restart{Policy: "on-failure", MaxRetries: -1} },
		"network":               func(s *Spec) { s.Network = "my net" },
	}
	for field, mutate := range cases {
		s := validSpec()
		mutate(&s)
		var fe *FieldError
		if err := s.Validate(); !errors.As(err, &fe) || fe.Field != field {
			t.Errorf("%s: Validate() = %v", field, err)
		}
	}
	s := validSpec()
	s.Network = "container:db"
	s.Ports = []Port{}
	if err := s.Validate(); err != nil {
		t.Errorf("network container:db: %v", err)
	}
}

// inspected is what Docker reports for a container created from nginx:1.29 with the form of validSpec plus an
// anonymous volume, an extra network with a static IP, a memory limit and a tmpfs mount.
func inspected() (container.InspectResponse, *image.InspectResponse) {
	img := &image.InspectResponse{ID: "sha256:nginx", Config: &dockerspec.DockerOCIImageConfig{ImageConfig: ocispec.ImageConfig{
		Env:          []string{"PATH=/usr/sbin:/usr/bin", "NGINX_VERSION=1.29.0"},
		Cmd:          []string{"nginx", "-g", "daemon off;"},
		Entrypoint:   []string{"/docker-entrypoint.sh"},
		ExposedPorts: map[string]struct{}{"80/tcp": {}},
		Labels:       map[string]string{"maintainer": "NGINX"},
		WorkingDir:   "",
		StopSignal:   "SIGQUIT",
	}}}
	anon := strings.Repeat("ab", 32)
	p80 := network.MustParsePort("80/tcp")
	p9000 := network.MustParsePort("9000/tcp")
	c := container.InspectResponse{
		ID: "0123456789abcdef0123456789abcdef", Name: "/web", Image: "sha256:nginx",
		State: &container.State{Running: true, Status: container.StateRunning},
		Config: &container.Config{
			Hostname: "0123456789ab", Image: "nginx:1.29",
			Env:          []string{"PATH=/usr/sbin:/usr/bin", "NGINX_VERSION=1.29.0", "TZ=Europe/Berlin"},
			Cmd:          []string{"nginx", "-g", "daemon off;"},
			Entrypoint:   []string{"/docker-entrypoint.sh"},
			ExposedPorts: network.PortSet{p80: {}, p9000: {}},
			Labels:       map[string]string{"maintainer": "NGINX", "app": "web"},
			StopSignal:   "SIGQUIT",
			Volumes:      map[string]struct{}{"/cache": {}},
		},
		HostConfig: &container.HostConfig{
			NetworkMode:   "front",
			PortBindings:  network.PortMap{p80: {{HostIP: netip.MustParseAddr("127.0.0.1"), HostPort: "8080"}}},
			RestartPolicy: container.RestartPolicy{Name: container.RestartPolicyUnlessStopped},
			Binds:         []string{"/srv/conf:/etc/nginx/conf.d:ro"},
			Mounts: []mount.Mount{
				{Type: mount.TypeVolume, Source: "web-data", Target: "/usr/share/nginx/html", VolumeOptions: &mount.VolumeOptions{NoCopy: true}},
				{Type: mount.TypeTmpfs, Target: "/run/nginx"},
			},
			CapAdd:    []string{"CAP_NET_ADMIN"},
			Resources: container.Resources{Memory: 256 << 20},
		},
		Mounts: []container.MountPoint{
			{Type: mount.TypeVolume, Name: "web-data", Destination: "/usr/share/nginx/html", RW: true},
			{Type: mount.TypeBind, Source: "/srv/conf", Destination: "/etc/nginx/conf.d", RW: false},
			{Type: mount.TypeVolume, Name: anon, Destination: "/cache", RW: true},
			{Type: mount.TypeTmpfs, Destination: "/run/nginx", RW: true},
		},
		NetworkSettings: &container.NetworkSettings{Networks: map[string]*network.EndpointSettings{
			"front": {Aliases: []string{"web"}},
			"back":  {Aliases: []string{"web-back", "0123456789ab"}, IPAMConfig: &network.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("10.10.0.5")}},
		}},
	}
	return c, img
}

func TestSpecOf(t *testing.T) {
	c, img := inspected()
	s := SpecOf(c, img)
	if s.Name != "web" || s.Image != "nginx:1.29" || s.Command != "" || s.Network != "front" || s.Restart.Policy != "unless-stopped" {
		t.Fatalf("spec %+v", s)
	}
	if len(s.Env) != 1 || s.Env[0] != (EnvVar{Name: "TZ", Value: "Europe/Berlin"}) {
		t.Fatalf("env %+v: values from the image must be left out", s.Env)
	}
	if len(s.Labels) != 1 || s.Labels[0] != (Label{Key: "app", Value: "web"}) {
		t.Fatalf("labels %+v: labels from the image must be left out", s.Labels)
	}
	if len(s.Ports) != 1 || s.Ports[0] != (Port{HostIP: "127.0.0.1", HostPort: 8080, ContainerPort: 80, Protocol: "tcp"}) {
		t.Fatalf("ports %+v", s.Ports)
	}
	if len(s.Mounts) != 3 {
		t.Fatalf("mounts %+v (bind, named and anonymous volume; tmpfs is not part of the form)", s.Mounts)
	}
	anon := s.Mounts[0] // sorted by target: /cache, /etc/nginx/conf.d, /usr/share/nginx/html
	if anon.Target != "/cache" || anon.Type != "volume" || !anon.Anonymous || s.Mounts[1].Type != "bind" || !s.Mounts[1].ReadOnly {
		t.Fatalf("mounts %+v", s.Mounts)
	}

	c.Config.Cmd = []string{"nginx", "-g", "daemon off; worker_processes 2;"}
	if got := SpecOf(c, img).Command; got != "nginx -g 'daemon off; worker_processes 2;'" {
		t.Fatalf("own command: %q", got)
	}
	if got := SpecOf(c, nil); len(got.Env) != 3 || got.Command == "" {
		t.Fatalf("without the image everything is the container's: %+v", got)
	}
}

// The form shows the labels of the container itself: not those of its image (same key and value), and not Docker's own
// (com.docker.compose.project, ...), which a recreation keeps without the form.
func TestSpecOfLabels(t *testing.T) {
	cases := []struct {
		name   string
		labels map[string]string // of the container; its image has maintainer=NGINX
		noImg  bool
		want   []Label
	}{
		{"the image's label with the same value is left out", map[string]string{"maintainer": "NGINX", "app": "web"},
			false, []Label{{Key: "app", Value: "web"}}},
		{"an image label with another value is the container's own", map[string]string{"maintainer": "ACME"},
			false, []Label{{Key: "maintainer", Value: "ACME"}}},
		{"an image label with an empty value instead of the image's", map[string]string{"maintainer": ""},
			false, []Label{{Key: "maintainer"}}},
		{"another key with the image's value is the container's own", map[string]string{"owner": "NGINX"},
			false, []Label{{Key: "owner", Value: "NGINX"}}},
		{"the labels of the namespaces of Docker are not shown", map[string]string{
			"com.docker.compose.project": "shop", "com.docker.compose.service": "web", "io.docker.x": "1", "org.dockerproject.y": "2", "app": "web"},
			false, []Label{{Key: "app", Value: "web"}}},
		{"sorted by key", map[string]string{"zone": "1", "app": "2", "Tier": "3", "mid": "4"},
			false, []Label{{Key: "Tier", Value: "3"}, {Key: "app", Value: "2"}, {Key: "mid", Value: "4"}, {Key: "zone", Value: "1"}}},
		{"an empty value is a label", map[string]string{"flag": ""}, false, []Label{{Key: "flag"}}},
		{"no labels", nil, false, []Label{}},
		{"only the image's labels", map[string]string{"maintainer": "NGINX"}, false, []Label{}},
		{"the image is gone: every label but Docker's is the container's own", map[string]string{
			"maintainer": "NGINX", "app": "web", "com.docker.compose.project": "shop"},
			true, []Label{{Key: "app", Value: "web"}, {Key: "maintainer", Value: "NGINX"}}},
	}
	for _, tc := range cases {
		c, img := inspected()
		c.Config.Labels = tc.labels
		if tc.noImg {
			img = nil
		}
		got := SpecOf(c, img).Labels
		if got == nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: labels %+v (nil %v), want %+v", tc.name, got, got == nil, tc.want)
		}
	}
	// an image without labels (or without a config) leaves nothing out
	c, img := inspected()
	img.Config.Labels = nil
	if got := SpecOf(c, img).Labels; !slices.Equal(got, []Label{{Key: "app", Value: "web"}, {Key: "maintainer", Value: "NGINX"}}) {
		t.Errorf("image without labels: %+v", got)
	}
	img.Config = nil
	if got := SpecOf(c, img).Labels; len(got) != 2 {
		t.Errorf("image without a config: %+v", got)
	}
	// a container without a config
	c.Config = nil
	if got := SpecOf(c, nil).Labels; got == nil || len(got) != 0 {
		t.Errorf("container without a config: %#v", got)
	}
}

// An explicit 0.0.0.0 or :: is what the container was created with and stays in the form: collapsing it to "" (every
// address) could widen the exposure when the container is recreated. A host port range is not a number: it shows as
// host port 0 (CreateOptions carries the range over).
func TestSpecOfKeepsExplicitHostIPsAndShowsRanges(t *testing.T) {
	c, img := inspected()
	c.HostConfig.PortBindings = network.PortMap{
		network.MustParsePort("53/udp"): {{HostPort: "5000-5010"}},
		network.MustParsePort("80/tcp"): {
			{HostIP: netip.MustParseAddr("::"), HostPort: "8080"},
			{HostIP: netip.MustParseAddr("0.0.0.0"), HostPort: "8080"},
			{HostPort: "8081"},
			{HostPort: ""},
		},
	}
	want := []Port{
		{ContainerPort: 53, Protocol: "udp"},
		{ContainerPort: 80, Protocol: "tcp"},
		{ContainerPort: 80, Protocol: "tcp", HostPort: 8081},
		{ContainerPort: 80, Protocol: "tcp", HostIP: "0.0.0.0", HostPort: 8080},
		{ContainerPort: 80, Protocol: "tcp", HostIP: "::", HostPort: 8080},
	}
	got := SpecOf(c, img).Ports
	if !slices.Equal(got, want) {
		t.Fatalf("ports\n got %+v\nwant %+v", got, want)
	}
	if err := (Spec{Name: "web", Image: "nginx", Ports: got}).Normalize().Validate(); err != nil {
		t.Fatalf("the ports of an inspected container must validate: %v", err)
	}
}

// The image goes into the URL path of a daemon request (see dockerapi.SafeRef): a ref that would be cleaned into another
// endpoint is no valid image.
func TestValidateRefusesUnsafeImages(t *testing.T) {
	for _, ref := range []string{"../containers/web", "/etc/passwd", "nginx/../../containers/web", "..", "nginx/..", "nginx?all=1", "nginx#frag"} {
		s := validSpec()
		s.Image = ref
		var fe *FieldError
		if err := s.Validate(); !errors.As(err, &fe) || fe.Field != "image" {
			t.Errorf("image %q: Validate() = %v", ref, err)
		}
	}
	for _, ref := range []string{"nginx:1.29", "ghcr.io/octo/app:v1.2", "localhost:5000/test/busybox:1.37", "my..image", "sha256:" + strings.Repeat("d", 64)} {
		s := validSpec()
		s.Image = ref
		if err := s.Validate(); err != nil {
			t.Errorf("image %q: %v", ref, err)
		}
	}
}

func TestSpecOfReadsTheLimits(t *testing.T) {
	c, img := inspected() // Memory 256 MiB
	c.HostConfig.NanoCPUs = 1_500_000_000
	s := SpecOf(c, img)
	if s.Resources != (Resources{Memory: 256 << 20, CPUs: 1.5}) {
		t.Fatalf("resources %+v", s.Resources)
	}
}
