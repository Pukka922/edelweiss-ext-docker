//go:build integration

package events

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
)

func TestEventsAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, cli)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.ShutdownWithTimeout(2 * time.Second) })

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+ln.Addr().String()+"/events", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	name := dockertest.Name(t, "evvol")
	dockertest.RemoveVolumeAtEnd(t, cli, name)
	go func() {
		time.Sleep(300 * time.Millisecond) // the subscription is set up by then
		_, _ = cli.VolumeCreate(context.Background(), client.VolumeCreateOptions{Name: name})
	}()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if strings.Contains(sc.Text(), `"type":"volume","action":"create","id":"`+name+`"`) {
			return
		}
	}
	t.Fatalf("no create event for volume %s: %v", name, sc.Err())
}
