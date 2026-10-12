package dockertest

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/registry"
	"github.com/moby/moby/client"
	"golang.org/x/crypto/bcrypt"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
)

// EnvHost names the throwaway daemon of make test-docker.
const EnvHost = "EDELWEISS_DOCKER_TEST_HOST"

// Images the integration tests use.
const (
	Busybox       = "busybox:1.37"
	RegistryImage = "registry:3.1.2"
)

// Live returns a client of the daemon in EnvHost. Without it the test is skipped: make test-docker sets it. Any value but
// tcp://127.0.0.1:<port> fails the test (see checkHost).
func Live(t testing.TB) *client.Client {
	t.Helper()
	host := os.Getenv(EnvHost)
	if host == "" {
		t.Skip(EnvHost + " is not set; run make test-docker")
	}
	if err := checkHost(host); err != nil {
		t.Fatal(err)
	}
	cli, err := dockerapi.New(host)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cli.Close() })
	return cli
}

// checkHost accepts only the form in which make test-docker passes its throwaway daemon, tcp://127.0.0.1:<port>, so that
// the integration tests can never reach another daemon (such as the one of this machine).
func checkHost(host string) error {
	rest, ok := strings.CutPrefix(host, "tcp://127.0.0.1:")
	port, err := strconv.Atoi(rest)
	if !ok || err != nil || port < 1 || port > 65535 || strconv.Itoa(port) != rest {
		return fmt.Errorf("%s=%q: the integration tests only talk to the throwaway daemon of make test-docker, "+
			"given as tcp://127.0.0.1:<port>; run make test-docker", EnvHost, host)
	}
	return nil
}

var counter atomic.Int64

// Name returns a name for a test object that no other test uses.
func Name(t testing.TB, prefix string) string {
	return fmt.Sprintf("edt-%s-%d-%d", prefix, os.Getpid(), counter.Add(1))
}

// Pull pulls ref into the daemon unless it is there already.
func Pull(t testing.TB, cli dockerapi.Docker, ref string) {
	t.Helper()
	ctx := context.Background()
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return
	}
	resp, err := cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	if err := resp.Wait(ctx); err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
}

// Run creates and starts a container (pulling its image first); it is removed with its volumes when the test ends.
func Run(t testing.TB, cli dockerapi.Docker, opts client.ContainerCreateOptions) string {
	t.Helper()
	Pull(t, cli, opts.Config.Image)
	ctx := context.Background()
	res, err := cli.ContainerCreate(ctx, opts)
	if err != nil {
		t.Fatalf("create %s: %v", opts.Name, err)
	}
	RemoveContainerAtEnd(t, cli, res.ID)
	if _, err := cli.ContainerStart(ctx, res.ID, client.ContainerStartOptions{}); err != nil {
		t.Fatalf("start %s: %v", opts.Name, err)
	}
	return res.ID
}

// RemoveContainerAtEnd removes the container (forced, with its anonymous volumes) when the test ends.
func RemoveContainerAtEnd(t testing.TB, cli dockerapi.Docker, id string) {
	t.Cleanup(func() {
		_, _ = cli.ContainerRemove(context.Background(), id, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	})
}

// RemoveVolumeAtEnd removes the volume when the test ends.
func RemoveVolumeAtEnd(t testing.TB, cli dockerapi.Docker, name string) {
	t.Cleanup(func() { _, _ = cli.VolumeRemove(context.Background(), name, client.VolumeRemoveOptions{Force: true}) })
}

// RemoveNetworkAtEnd removes the network when the test ends.
func RemoveNetworkAtEnd(t testing.TB, cli dockerapi.Docker, id string) {
	t.Cleanup(func() { _, _ = cli.NetworkRemove(context.Background(), id, client.NetworkRemoveOptions{}) })
}

// Registry runs registry:3.1.2 in the test daemon, published on port. The daemon reaches it as localhost:<port>, which
// it treats as an insecure (plain HTTP) registry. With user set, the registry requires that login (htpasswd with a
// bcrypt hash, the only format registry:3 accepts). It returns the address.
func Registry(t testing.TB, cli dockerapi.Docker, port int, user, password string) string {
	t.Helper()
	cfg := &container.Config{Image: RegistryImage}
	if user != "" {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Env = []string{
			"HTPASSWD=" + user + ":" + string(hash),
			"REGISTRY_AUTH=htpasswd",
			"REGISTRY_AUTH_HTPASSWD_REALM=edelweiss-test",
			"REGISTRY_AUTH_HTPASSWD_PATH=/etc/distribution/htpasswd",
		}
		cfg.Entrypoint = []string{"/bin/sh", "-c"}
		cfg.Cmd = []string{`printf '%s\n' "$HTPASSWD" > /etc/distribution/htpasswd && exec /entrypoint.sh /etc/distribution/config.yml`}
	}
	p := network.MustParsePort("5000/tcp")
	cfg.ExposedPorts = network.PortSet{p: {}}
	host := &container.HostConfig{PortBindings: network.PortMap{p: {{HostIP: netip.Addr{}, HostPort: strconv.Itoa(port)}}}}
	Run(t, cli, client.ContainerCreateOptions{Name: Name(t, "registry"), Config: cfg, HostConfig: host})
	addr := "localhost:" + strconv.Itoa(port)
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, err := cli.RegistryLogin(context.Background(), client.RegistryLoginOptions{ServerAddress: addr, Username: user, Password: password})
		if err == nil {
			return addr
		}
		if time.Now().After(deadline) {
			t.Fatalf("registry %s did not come up: %v", addr, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// Exec runs cmd in the running container id and returns its standard output (standard error goes to the test log).
func Exec(t testing.TB, cli dockerapi.Docker, id string, cmd ...string) string {
	t.Helper()
	ctx := context.Background()
	ex, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{Cmd: cmd, AttachStdout: true, AttachStderr: true})
	if err != nil {
		t.Fatalf("exec %v: %v", cmd, err)
	}
	att, err := cli.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{})
	if err != nil {
		t.Fatalf("exec %v: %v", cmd, err)
	}
	defer att.Close()
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, att.Reader); err != nil {
		t.Fatalf("exec %v: %v", cmd, err)
	}
	if stderr.Len() > 0 {
		t.Logf("exec %v: stderr: %s", cmd, stderr.String())
	}
	return stdout.String()
}

// PushBusybox tags busybox as <registry>/test/busybox:1.37, pushes it with the given login and removes the local tag
// again; it returns the pushed reference.
func PushBusybox(t testing.TB, cli *client.Client, reg, user, password string) string {
	t.Helper()
	ctx := context.Background()
	Pull(t, cli, Busybox)
	ref := reg + "/test/busybox:1.37"
	if _, err := cli.ImageTag(ctx, client.ImageTagOptions{Source: Busybox, Target: ref}); err != nil {
		t.Fatal(err)
	}
	auth, err := authconfig.Encode(registry.AuthConfig{Username: user, Password: password, ServerAddress: reg})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := cli.ImagePush(ctx, ref, client.ImagePushOptions{RegistryAuth: auth})
	if err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
	if err := resp.Wait(ctx); err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
	if _, err := cli.ImageRemove(ctx, ref, client.ImageRemoveOptions{PruneChildren: true}); err != nil {
		t.Fatal(err)
	}
	return ref
}
