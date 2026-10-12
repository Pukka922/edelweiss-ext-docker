package containers

import (
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/mount"
	"github.com/moby/moby/api/types/network"
)

func TestCreateOptionsForANewContainer(t *testing.T) {
	s := validSpec()
	s.Command = "nginx -g 'daemon off;'"
	opts, extra, err := CreateOptions(s, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, host := opts.Config, opts.HostConfig
	p80 := network.MustParsePort("80/tcp")
	if opts.Name != "web" || cfg.Image != "nginx:1.29" || !slices.Equal(cfg.Cmd, []string{"nginx", "-g", "daemon off;"}) ||
		!slices.Equal(cfg.Env, []string{"TZ=Europe/Berlin"}) || len(extra) != 0 {
		t.Fatalf("options %+v %+v", opts, cfg)
	}
	if _, ok := cfg.ExposedPorts[p80]; !ok || host.PortBindings[p80][0].HostPort != "8080" || host.PortBindings[p80][0].HostIP.IsValid() {
		t.Fatalf("ports %+v %+v", cfg.ExposedPorts, host.PortBindings)
	}
	if string(host.NetworkMode) != "bridge" || host.RestartPolicy.Name != container.RestartPolicyUnlessStopped || len(host.Mounts) != 2 || host.Binds != nil {
		t.Fatalf("host config %+v", host)
	}
	// the order of the form: the volume comes first, the bind mount second
	if host.Mounts[0].Type != mount.TypeVolume || host.Mounts[0].Source != "web-data" ||
		host.Mounts[1].Type != mount.TypeBind || host.Mounts[1].Source != "/srv/conf" || !host.Mounts[1].ReadOnly {
		t.Fatalf("mounts %+v", host.Mounts)
	}
}

func TestCreateOptionsForARecreation(t *testing.T) {
	c, img := inspected()
	s := SpecOf(c, img)
	s.Image = "nginx:1.30"
	s.Env = append(s.Env, EnvVar{Name: "DEBUG", Value: "1"})
	s = s.Normalize() // as the handlers do before CreateOptions
	opts, extra, err := CreateOptions(s, &c, img)
	if err != nil {
		t.Fatal(err)
	}
	cfg, host := opts.Config, opts.HostConfig

	// values from the old image are not frozen
	if cfg.Cmd != nil || cfg.Entrypoint != nil || cfg.StopSignal != "" || cfg.Hostname != "" {
		t.Fatalf("image values kept: cmd %v entrypoint %v stop %q hostname %q", cfg.Cmd, cfg.Entrypoint, cfg.StopSignal, cfg.Hostname)
	}
	if !slices.Equal(cfg.Env, []string{"TZ=Europe/Berlin", "DEBUG=1"}) || len(cfg.Labels) != 1 || cfg.Labels["app"] != "web" {
		t.Fatalf("env %v labels %v", cfg.Env, cfg.Labels)
	}
	p9000 := network.MustParsePort("9000/tcp")
	if _, ok := cfg.ExposedPorts[p9000]; !ok || len(cfg.ExposedPorts) != 2 {
		t.Fatalf("exposed ports %v: 9000 (own, not published) stays, 80 comes from the form", cfg.ExposedPorts)
	}
	if cfg.Volumes != nil {
		t.Fatalf("volumes %v", cfg.Volumes)
	}

	// what the form does not show stays
	if host.Memory != 256<<20 || !slices.Equal(host.CapAdd, []string{"CAP_NET_ADMIN"}) {
		t.Fatalf("resources/capabilities lost: %+v", host)
	}
	var tmpfs, named, anon bool
	for _, m := range host.Mounts {
		switch {
		case m.Type == mount.TypeTmpfs && m.Target == "/run/nginx":
			tmpfs = true
		case m.Type == mount.TypeVolume && m.Source == "web-data":
			named = m.VolumeOptions != nil && m.VolumeOptions.NoCopy // the options of the original mount are kept
		case m.Type == mount.TypeVolume && m.Target == "/cache":
			anon = len(m.Source) == 64 // the anonymous volume is taken over by name: its data stays
		}
	}
	// the bind mount stays a bind, as it was created with -v (and is not in the mounts as well)
	if !tmpfs || !named || !anon || len(host.Mounts) != 3 || !slices.Equal(host.Binds, []string{"/srv/conf:/etc/nginx/conf.d:ro"}) {
		t.Fatalf("mounts %+v binds %v", host.Mounts, host.Binds)
	}

	// networks: the primary one goes into the create call, the others are connected afterwards
	if string(host.NetworkMode) != "front" || opts.NetworkingConfig == nil || !slices.Equal(opts.NetworkingConfig.EndpointsConfig["front"].Aliases, []string{"web"}) {
		t.Fatalf("primary network %+v", opts.NetworkingConfig)
	}
	back, ok := extra["back"]
	if !ok || len(extra) != 1 || back.IPAMConfig.IPv4Address.String() != "10.10.0.5" || !slices.Equal(back.Aliases, []string{"web-back"}) {
		t.Fatalf("extra networks %+v (the alias that is the old short ID is dropped)", extra)
	}

	// network host: no other networks
	s.Network, s.Ports = "host", []Port{}
	if _, extra, _ := CreateOptions(s, &c, img); len(extra) != 0 {
		t.Fatalf("extra networks with network host: %v", extra)
	}
}

// A new network in the form replaces the old primary network (HostConfig.NetworkMode): only the further networks are
// connected again, with their aliases and fixed IPs.
func TestCreateOptionsReplacesThePrimaryNetwork(t *testing.T) {
	c, img := inspected() // network mode front, connected to front and back
	s := SpecOf(c, img)
	s.Network = "other"
	opts, extra, err := CreateOptions(s.Normalize(), &c, img)
	if err != nil {
		t.Fatal(err)
	}
	if string(opts.HostConfig.NetworkMode) != "other" || opts.NetworkingConfig != nil {
		t.Fatalf("network mode %q, networking config %+v", opts.HostConfig.NetworkMode, opts.NetworkingConfig)
	}
	if _, ok := extra["front"]; ok || len(extra) != 1 || extra["back"] == nil || extra["back"].IPAMConfig.IPv4Address.String() != "10.10.0.5" ||
		!slices.Equal(extra["back"].Aliases, []string{"web-back"}) {
		t.Fatalf("extra networks %+v, want back alone (front was the old primary network)", extra)
	}

	// the further network becomes the primary one: it keeps its settings in the create call, the old primary one is gone
	s.Network = "back"
	opts, extra, _ = CreateOptions(s.Normalize(), &c, img)
	if len(extra) != 0 || opts.NetworkingConfig == nil || opts.NetworkingConfig.EndpointsConfig["back"].IPAMConfig.IPv4Address.String() != "10.10.0.5" {
		t.Fatalf("back as primary network: extra %+v, networking config %+v", extra, opts.NetworkingConfig)
	}

	// network mode default is the network bridge
	c.HostConfig.NetworkMode = "default"
	c.NetworkSettings.Networks = map[string]*network.EndpointSettings{"bridge": {}, "back": c.NetworkSettings.Networks["back"]}
	s.Network = "other"
	if _, extra, _ := CreateOptions(s.Normalize(), &c, img); len(extra) != 1 || extra["back"] == nil {
		t.Fatalf("from network mode default: extra %+v, want back alone", extra)
	}
}

func TestCreateOptionsLeavesBaseUntouched(t *testing.T) {
	c, _ := inspected()
	before := len(c.Config.ExposedPorts)
	s := SpecOf(c, nil)
	s.Ports = append(s.Ports, Port{ContainerPort: 81, Protocol: "tcp"})
	// without the image nothing is unfrozen, and the options must still not share the exposed ports with base
	opts, _, err := CreateOptions(s.Normalize(), &c, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := opts.Config.ExposedPorts[network.MustParsePort("81/tcp")]; !ok {
		t.Fatalf("exposed ports %v: 81 is missing", opts.Config.ExposedPorts)
	}
	if len(c.Config.ExposedPorts) != before {
		t.Fatalf("base exposed ports changed: %v", c.Config.ExposedPorts)
	}
}

// withBinds is inspected() as a container created with -v: its volume and bind mounts live in HostConfig.Binds, with
// options (propagation, SELinux label, nocopy) that mount.Mount cannot all express.
func withBinds() (container.InspectResponse, *image.InspectResponse) {
	c, img := inspected()
	c.HostConfig.Binds = []string{"/:/host:ro,rslave", "/srv/conf:/etc/nginx/conf.d:z", "data:/data:nocopy"}
	c.HostConfig.Mounts = []mount.Mount{{Type: mount.TypeTmpfs, Target: "/run/nginx"}}
	c.Mounts = []container.MountPoint{
		{Type: mount.TypeBind, Source: "/", Destination: "/host", RW: false},
		{Type: mount.TypeBind, Source: "/srv/conf", Destination: "/etc/nginx/conf.d", RW: true},
		{Type: mount.TypeVolume, Name: "data", Destination: "/data", RW: true},
		{Type: mount.TypeTmpfs, Destination: "/run/nginx", RW: true},
	}
	return c, img
}

func TestCreateOptionsKeepsTheBindsOfARecreation(t *testing.T) {
	// the form is sorted by target: /data, /etc/nginx/conf.d, /host
	const data, conf, host = "data:/data:nocopy", "/srv/conf:/etc/nginx/conf.d:z", "/:/host:ro,rslave"
	recreate := func(mutate func(*Spec)) ([]string, []mount.Mount) {
		t.Helper()
		c, img := withBinds()
		s := SpecOf(c, img)
		if mutate != nil {
			mutate(&s)
		}
		opts, _, err := CreateOptions(s.Normalize(), &c, img)
		if err != nil {
			t.Fatal(err)
		}
		binds, mounts := opts.HostConfig.Binds, opts.HostConfig.Mounts
		seen := map[string]bool{}
		for _, b := range binds {
			target := strings.Split(b, ":")[1]
			if seen[target] {
				t.Errorf("target %s twice in %v", target, binds)
			}
			seen[target] = true
		}
		for _, m := range mounts {
			if seen[m.Target] {
				t.Errorf("target %s in the binds %v and in the mounts %+v", m.Target, binds, mounts)
			}
			seen[m.Target] = true
		}
		return binds, mounts
	}
	readOnly := func(target string, ro bool) func(*Spec) {
		return func(s *Spec) {
			for i := range s.Mounts {
				if s.Mounts[i].Target == target {
					s.Mounts[i].ReadOnly = ro
				}
			}
		}
	}
	onlyTmpfs := func(mounts []mount.Mount) bool {
		return len(mounts) == 1 && mounts[0].Type == mount.TypeTmpfs && mounts[0].Target == "/run/nginx"
	}

	binds, mounts := recreate(nil)
	if !slices.Equal(binds, []string{data, conf, host}) || !onlyTmpfs(mounts) {
		t.Fatalf("unchanged form: binds %v mounts %+v", binds, mounts)
	}

	// only the ro/rw part follows the form: replaced where it was, added at the end where there was none
	for name, c := range map[string]struct {
		mutate func(*Spec)
		want   []string
	}{
		"rw":        {readOnly("/host", false), []string{data, conf, "/:/host:rw,rslave"}},
		"ro":        {readOnly("/etc/nginx/conf.d", true), []string{data, "/srv/conf:/etc/nginx/conf.d:z,ro", host}},
		"ro volume": {readOnly("/data", true), []string{"data:/data:nocopy,ro", conf, host}},
	} {
		binds, mounts := recreate(c.mutate)
		if !slices.Equal(binds, c.want) || !onlyTmpfs(mounts) {
			t.Errorf("%s: binds %v mounts %+v, want %v", name, binds, mounts, c.want)
		}
	}

	// a changed target (or source, or type) is a new mount: it goes to the mounts, without the options of the old one
	binds, mounts = recreate(func(s *Spec) {
		for i := range s.Mounts {
			if s.Mounts[i].Target == "/data" {
				s.Mounts[i].Target = "/data2"
			}
		}
	})
	if !slices.Equal(binds, []string{conf, host}) || len(mounts) != 2 {
		t.Fatalf("moved mount: binds %v mounts %+v", binds, mounts)
	}
	if m := mounts[0]; m.Type != mount.TypeVolume || m.Source != "data" || m.Target != "/data2" || m.VolumeOptions != nil {
		t.Fatalf("moved mount %+v", m)
	}
	binds, mounts = recreate(func(s *Spec) {
		for i := range s.Mounts {
			if s.Mounts[i].Target == "/etc/nginx/conf.d" {
				s.Mounts[i].Source = "/srv/other"
			}
		}
	})
	if !slices.Equal(binds, []string{data, host}) || len(mounts) != 2 || mounts[0].Type != mount.TypeBind || mounts[0].Source != "/srv/other" {
		t.Fatalf("other source: binds %v mounts %+v", binds, mounts)
	}

	// a mount removed in the form is gone
	binds, mounts = recreate(func(s *Spec) {
		s.Mounts = slices.DeleteFunc(s.Mounts, func(m Mount) bool { return m.Target == "/host" })
	})
	if !slices.Equal(binds, []string{data, conf}) || !onlyTmpfs(mounts) {
		t.Fatalf("removed mount: binds %v mounts %+v", binds, mounts)
	}

	// without a base there are no binds
	if opts, _, err := CreateOptions(validSpec(), nil, nil); err != nil || opts.HostConfig.Binds != nil {
		t.Fatalf("new container: %v %v", opts.HostConfig.Binds, err)
	}
}

func TestBuildMountsBindOptions(t *testing.T) {
	cases := []struct {
		bind     string
		readOnly bool
		want     string
	}{
		{"/a:/b:rw,z", false, "/a:/b:rw,z"},
		{"/a:/b:rw,z", true, "/a:/b:ro,z"},
		{"/a:/b:ro", true, "/a:/b:ro"},
		{"/a:/b:ro", false, "/a:/b:rw"},
		{"/a:/b", false, "/a:/b"},
		{"/a:/b", true, "/a:/b:ro"},
		{"/a:/b:Z,ro,rslave", false, "/a:/b:Z,rw,rslave"},
		{"/a:/b:rprivate,Z", true, "/a:/b:rprivate,Z,ro"},
		{"vol:/b:nocopy", false, "vol:/b:nocopy"},
	}
	for _, c := range cases {
		parts := strings.Split(c.bind, ":")
		typ := "bind"
		if !strings.HasPrefix(parts[0], "/") {
			typ = "volume"
		}
		binds, mounts := buildMounts([]Mount{{Type: typ, Source: parts[0], Target: "/b", ReadOnly: c.readOnly}}, []string{c.bind}, nil)
		if !slices.Equal(binds, []string{c.want}) || len(mounts) != 0 {
			t.Errorf("%s (readOnly %v): binds %v mounts %+v, want %s", c.bind, c.readOnly, binds, mounts, c.want)
		}
	}

	// a bind given with trailing slashes (-v /srv/conf/:/etc/x/:z) is the form's cleaned mount: it keeps its options
	binds, mounts := buildMounts([]Mount{{Type: "bind", Source: "/srv/conf", Target: "/etc/x"}}, []string{"/srv/conf/:/etc/x/:z"}, nil)
	if !slices.Equal(binds, []string{"/srv/conf:/etc/x:z"}) || len(mounts) != 0 {
		t.Fatalf("trailing slashes: binds %v mounts %+v", binds, mounts)
	}
	binds, mounts = buildMounts([]Mount{{Type: "volume", Source: "data", Target: "/data"}}, []string{"data:/data/:nocopy"}, nil)
	if !slices.Equal(binds, []string{"data:/data:nocopy"}) || len(mounts) != 0 {
		t.Fatalf("volume with a trailing slash in the target: binds %v mounts %+v", binds, mounts)
	}

	// the entry of an anonymous volume has no source: nothing to keep, the form's mount (by name) is a mount
	anon := strings.Repeat("cd", 32)
	binds, mounts = buildMounts([]Mount{{Type: "volume", Source: anon, Target: "/cache"}}, []string{"/cache"}, nil)
	if len(binds) != 0 || len(mounts) != 1 || mounts[0].Source != anon {
		t.Fatalf("anonymous volume: binds %v mounts %+v", binds, mounts)
	}
	// an entry of the other kind (volume vs. path) with the same text does not match
	binds, mounts = buildMounts([]Mount{{Type: "volume", Source: "data", Target: "/b"}}, []string{"/data:/b:z"}, nil)
	if len(binds) != 0 || len(mounts) != 1 {
		t.Fatalf("other type: binds %v mounts %+v", binds, mounts)
	}
}

func TestCreateOptionsReturnsFieldErrorsInsteadOfPanicking(t *testing.T) {
	cases := map[string]Port{
		"host ip not an address":  {ContainerPort: 80, Protocol: "tcp", HostIP: "localhost"},
		"container port 0":        {ContainerPort: 0, Protocol: "tcp"},
		"negative container port": {ContainerPort: -1, Protocol: "tcp"},
		"container port 65536":    {ContainerPort: 65536, Protocol: "tcp"}, // 0 as a uint16
		"container port 70000":    {ContainerPort: 70000, Protocol: "tcp"}, // 4464 as a uint16
		"host port too big":       {ContainerPort: 80, Protocol: "tcp", HostPort: 70000},
		"negative host port":      {ContainerPort: 80, Protocol: "tcp", HostPort: -1},
		"no protocol":             {ContainerPort: 80},
	}
	for name, p := range cases {
		s := validSpec()
		s.Ports = []Port{p} // not normalized and not validated: callers must, but CreateOptions must not panic
		var fe *FieldError
		if _, _, err := CreateOptions(s, nil, nil); !errors.As(err, &fe) || fe.Field != "ports" {
			t.Errorf("%s: CreateOptions() = %v, want a FieldError for ports", name, err)
		}
	}
	s := validSpec()
	s.Command = "sh -c 'unterminated"
	var fe *FieldError
	if _, _, err := CreateOptions(s, nil, nil); !errors.As(err, &fe) || fe.Field != "command" {
		t.Errorf("command: CreateOptions() = %v", err)
	}
}

// The form shows a host port range (8080-8090) as host port 0. On a recreation the range stays, but only for the same
// container port, protocol and host IP, and not where the person typed a port or cleared a single one.
func TestCreateOptionsCarriesHostPortRangesOver(t *testing.T) {
	p53, p80, p443 := network.MustParsePort("53/udp"), network.MustParsePort("80/tcp"), network.MustParsePort("443/tcp")
	all, loopback := netip.MustParseAddr("0.0.0.0"), netip.MustParseAddr("127.0.0.1")
	recreate := func(mutate func(*Spec)) network.PortMap {
		t.Helper()
		c, img := inspected()
		c.HostConfig.PortBindings = network.PortMap{
			p53:  {{HostIP: loopback, HostPort: "5000-5010"}},
			p80:  {{HostPort: "8080-8090"}, {HostIP: all, HostPort: "9000"}},
			p443: {{HostPort: "8443"}},
		}
		s := SpecOf(c, img)
		if mutate != nil {
			mutate(&s)
		}
		opts, _, err := CreateOptions(s.Normalize(), &c, img)
		if err != nil {
			t.Fatal(err)
		}
		return opts.HostConfig.PortBindings
	}
	setHostPort := func(containerPort int, hostIP string, hostPort int) func(*Spec) {
		return func(s *Spec) {
			for i, p := range s.Ports {
				if p.ContainerPort == containerPort && p.HostIP == hostIP {
					s.Ports[i].HostPort = hostPort
				}
			}
		}
	}

	got := recreate(nil)
	want := network.PortMap{
		p53:  {{HostIP: loopback, HostPort: "5000-5010"}},
		p80:  {{HostPort: "8080-8090"}, {HostIP: all, HostPort: "9000"}},
		p443: {{HostPort: "8443"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unchanged form:\n got %v\nwant %v", got, want)
	}
	if got := recreate(setHostPort(80, "", 8081)); got[p80][0].HostPort != "8081" {
		t.Errorf("a typed port replaces the range: %v", got[p80])
	}
	if got := recreate(setHostPort(443, "", 0)); got[p443][0].HostPort != "" {
		t.Errorf("a single port that is cleared lets Docker choose, it is not brought back: %v", got[p443])
	}
	if got := recreate(func(s *Spec) {
		for i := range s.Ports {
			if s.Ports[i].ContainerPort == 53 {
				s.Ports[i].HostIP = "10.0.0.1"
			}
		}
	}); got[p53][0].HostPort != "" || got[p53][0].HostIP != netip.MustParseAddr("10.0.0.1") {
		t.Errorf("another host IP is another binding, the range does not follow: %v", got[p53])
	}
	// two entries of the form with host port 0 do not both get the one range (the added one comes last, after the one on 0.0.0.0)
	if got := recreate(func(s *Spec) { s.Ports = append(s.Ports, Port{ContainerPort: 80, Protocol: "tcp"}) }); len(got[p80]) != 3 || got[p80][0].HostPort != "8080-8090" || got[p80][2].HostPort != "" {
		t.Errorf("a range is carried over once: %v", got[p80])
	}
	// a new container has no ranges
	s := validSpec()
	s.Ports = []Port{{ContainerPort: 80, Protocol: "tcp"}}
	if opts, _, err := CreateOptions(s, nil, nil); err != nil || opts.HostConfig.PortBindings[p80][0].HostPort != "" {
		t.Errorf("new container: %v %v", opts.HostConfig.PortBindings, err)
	}
}
