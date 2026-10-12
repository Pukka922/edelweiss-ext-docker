//go:build integration

package containers

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
)

// serve runs the container routes on a real listener (an SSE stream of a running container does not end, so
// app.Test cannot read it) and returns the base URL.
func serve(t *testing.T, s *Service) string {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, s)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.ShutdownWithTimeout(2 * time.Second) })
	return "http://" + ln.Addr().String()
}

// waitEvent reads the SSE stream at url until an event named name has data containing want.
func waitEvent(t *testing.T, url, name, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set(sdk.HeaderUser, "anna")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	current := ""
	for sc.Scan() {
		line := sc.Text()
		if n, ok := strings.CutPrefix(line, "event: "); ok {
			current = n
		}
		if d, ok := strings.CutPrefix(line, "data: "); ok && current == name && strings.Contains(d, want) {
			return
		}
	}
	t.Fatalf("no %s event with %q at %s: %v", name, want, url, sc.Err())
}

func TestLogsAndStatsAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	id := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: dockertest.Name(t, "logs"),
		Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sh", "-c", "echo out-line; echo err-line >&2; exec sleep 300"}}})
	s := New(cli)
	s.StatsInterval = time.Second
	base := serve(t, s)
	waitEvent(t, base+"/containers/"+id+"/logs", "line", `"line":"out-line"`)
	waitEvent(t, base+"/containers/"+id+"/logs", "line", `"stream":"stderr"`)
	waitEvent(t, base+"/containers/"+id+"/stats", "stats", `"pids":`)
}

// The statistics of a container that stops end with a stopped event, and a stream of a stopped container says so at once.
func TestStatsEndWhenContainerStops(t *testing.T) {
	cli := dockertest.Live(t)
	id := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: dockertest.Name(t, "stats"),
		Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sleep", "300"}}})
	s := New(cli)
	s.StatsInterval = time.Second
	base := serve(t, s)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/containers/"+id+"/stats", nil)
	req.Header.Set(sdk.HeaderUser, "anna")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var events []string
	stopping := false
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		name, ok := strings.CutPrefix(sc.Text(), "event: ")
		if !ok {
			continue
		}
		events = append(events, name)
		if name == "stats" && !stopping { // sleep as PID 1 ignores SIGTERM: the stop ends in a kill after one second
			stopping = true
			timeout := 1
			go func() {
				_, _ = cli.ContainerStop(context.Background(), id, client.ContainerStopOptions{Timeout: &timeout})
			}()
		}
		if name == "stopped" {
			break
		}
	}
	if len(events) < 2 || events[len(events)-1] != "stopped" {
		t.Fatalf("events %v: want stats and then stopped (%v)", events, sc.Err())
	}
	waitEvent(t, base+"/containers/"+id+"/stats", "stopped", "{}")
}

// The logs of a container that exits end with an end event.
func TestLogsEndWhenContainerExits(t *testing.T) {
	cli := dockertest.Live(t)
	id := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: dockertest.Name(t, "bye"),
		Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sh", "-c", "echo bye"}}})
	base := serve(t, New(cli))
	waitEvent(t, base+"/containers/"+id+"/logs", "line", `"line":"bye"`)
	waitEvent(t, base+"/containers/"+id+"/logs", "end", "{}")
}

// The daemon cuts a line of more than 16 KiB into parts, each with its own timestamp: the browser gets one line, without
// the timestamps of the later parts. (Only ASCII: the daemon's json-file driver cuts at bytes and replaces a rune that
// is cut in two with U+FFFD before the logs are read, so such a line is damaged before it reaches the extension.)
func TestLogsLongLineAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	id := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: dockertest.Name(t, "long"),
		Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sh", "-c", `yes a | head -n 40000 | tr -d '\n'; echo`}}})
	base := serve(t, New(cli))
	waitEvent(t, base+"/containers/"+id+"/logs", "line", `"line":"`+strings.Repeat("a", 40000)+`"`)
}
