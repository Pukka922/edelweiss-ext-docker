package dockertest

import "testing"

// The integration tests talk only to the throwaway daemon of make test-docker, on a port of 127.0.0.1: any other value of
// EnvHost could point them at a real daemon, whose containers, images and volumes they would change.
func TestCheckHost(t *testing.T) {
	for _, ok := range []string{"tcp://127.0.0.1:2375", "tcp://127.0.0.1:1", "tcp://127.0.0.1:65535"} {
		if err := checkHost(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{
		"unix:///var/run/docker.sock", "tcp://localhost:2375", "tcp://10.0.0.1:2375", "tcp://0.0.0.0:2375", "tcp://[::1]:2375",
		"http://127.0.0.1:2375", "tcp://127.0.0.1", "tcp://127.0.0.1:", "tcp://127.0.0.1:abc", "tcp://127.0.0.1:0",
		"tcp://127.0.0.1:65536", "tcp://127.0.0.1:+2375", "tcp://127.0.0.1:02375", "tcp://127.0.0.1:2375/", "tcp://127.0.0.1:2375?x",
		"tcp://127.0.0.1.example.com:2375", " tcp://127.0.0.1:2375",
	} {
		if err := checkHost(bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
