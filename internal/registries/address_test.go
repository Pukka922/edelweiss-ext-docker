package registries

import "testing"

func TestNormalizeAddress(t *testing.T) {
	good := map[string]string{
		"registry.example.com":        "registry.example.com",
		"  Registry.Example.COM/ ":    "registry.example.com",
		"https://ghcr.io/v2/":         "ghcr.io",
		"http://localhost:5000":       "localhost:5000",
		"192.168.1.10:5000":           "192.168.1.10:5000",
		"docker.io":                   DockerHub,
		"https://index.docker.io/v1/": DockerHub,
		"registry-1.docker.io":        DockerHub,
		"docker.io:443":               "docker.io:443",
	}
	for in, want := range good {
		if got, err := NormalizeAddress(in); err != nil || got != want {
			t.Errorf("NormalizeAddress(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "   ", "https://", "bad host", "host:0", "host:65536", "host:08", "-x.example", "x_y.com"} {
		if got, err := NormalizeAddress(in); err == nil {
			t.Errorf("NormalizeAddress(%q) = %q, want an error", in, got)
		}
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"nginx":                          DockerHub,
		"library/nginx:1.29":             DockerHub,
		"pukka/app":                      DockerHub,
		"ghcr.io/pukka/app:1":            "ghcr.io",
		"localhost:5000/x/y:z":           "localhost:5000",
		"localhost/app":                  "localhost",
		"Registry.Example.com:5000/a/b":  "registry.example.com:5000",
		"index.docker.io/library/alpine": DockerHub,
	}
	for ref, want := range cases {
		if got := HostOf(ref); got != want {
			t.Errorf("HostOf(%q) = %q, want %q", ref, got, want)
		}
	}
}

func TestServerAddress(t *testing.T) {
	if got := ServerAddress(DockerHub); got != "https://index.docker.io/v1/" {
		t.Fatalf("docker hub: %q", got)
	}
	if got := ServerAddress("ghcr.io"); got != "ghcr.io" {
		t.Fatalf("ghcr: %q", got)
	}
}
