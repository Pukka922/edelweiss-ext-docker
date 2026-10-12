package events

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	dockerevents "github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

func TestEvents(t *testing.T) {
	var filters client.Filters
	f := &dockertest.Fake{EventsFn: func(_ context.Context, o client.EventsListOptions) client.EventsResult {
		filters = o.Filters
		msgs := make(chan dockerevents.Message)
		errs := make(chan error, 1)
		go func() {
			for _, m := range []dockerevents.Message{
				{Type: "container", Action: "start", Actor: dockerevents.Actor{ID: "c1"}},
				{Type: "container", Action: "exec_start: sh -c ls", Actor: dockerevents.Actor{ID: "c1"}},
				{Type: "container", Action: "attach", Actor: dockerevents.Actor{ID: "c1"}},
				{Type: "container", Action: "health_status: healthy", Actor: dockerevents.Actor{ID: "c1"}},
				{Type: "volume", Action: "create", Actor: dockerevents.Actor{ID: "data"}},
			} {
				msgs <- m
			}
			errs <- io.EOF
		}()
		return client.EventsResult{Messages: msgs, Err: errs}
	}}
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, f)
	resp := sdktest.Do(t, app, sdktest.Request("GET", "/events", nil, sdk.User{Name: "anna"}))
	var data []string
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if d, ok := strings.CutPrefix(sc.Text(), "data: "); ok {
			data = append(data, d)
		}
	}
	want := []string{
		`{"type":"container","action":"start","id":"c1"}`,
		`{"type":"container","action":"health_status","id":"c1"}`,
		`{"type":"volume","action":"create","id":"data"}`,
	}
	if !slices.Equal(data, want) {
		t.Fatalf("events\n %v\nwant\n %v", data, want)
	}
	for _, typ := range []string{"container", "image", "volume", "network"} {
		if !filters["type"][typ] {
			t.Errorf("type %s not subscribed: %v", typ, filters)
		}
	}
}

// An idle stream does not end by itself, so app.Test (which reads a response to its end) cannot be used: the route runs
// on a real listener. The stream opens before the first event (the browser's EventSource and a proxy in front need
// the response headers), and the context of the Docker call ends when the browser leaves.
func TestEventsStreamOpensAndEnds(t *testing.T) {
	release := make(chan struct{}) // the fake daemon is silent until it is closed
	ended := make(chan struct{})   // closed when the context of the Docker call ends
	f := &dockertest.Fake{EventsFn: func(ctx context.Context, _ client.EventsListOptions) client.EventsResult {
		msgs := make(chan dockerevents.Message)
		errs := make(chan error, 1)
		go func() {
			select {
			case <-release:
			case <-ctx.Done():
			}
			for { // events until the Docker call is cancelled: the first write to a closed connection may still succeed
				select {
				case msgs <- dockerevents.Message{Type: "container", Action: "start", Actor: dockerevents.Actor{ID: "c1"}}:
					time.Sleep(10 * time.Millisecond)
				case <-ctx.Done():
					close(ended)
					errs <- ctx.Err()
					return
				}
			}
		}()
		return client.EventsResult{Messages: msgs, Err: errs}
	}}
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, f)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.ShutdownWithTimeout(2 * time.Second) })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://"+ln.Addr().String()+"/events", nil)
	resp, err := http.DefaultClient.Do(req) // answers with the headers, while no event has happened yet
	if err != nil {
		t.Fatalf("the stream did not open before the first event: %v", err)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("content type %q", ct)
	}
	if first, err := bufio.NewReader(resp.Body).ReadString('\n'); err != nil || !strings.HasPrefix(first, ":") {
		t.Errorf("first line %q (%v), want a comment", first, err)
	}
	_ = resp.Body.Close() // the browser leaves
	close(release)
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the context of the Docker call did not end when the browser left")
	}
}
