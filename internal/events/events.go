// Package events forwards Docker's events about containers, images, volumes and networks to the browser, so that its
// lists update when something changes, also through the docker command line.
package events

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss/sdk"
)

// Event is what the browser gets for a change.
type Event struct {
	Type   string `json:"type"`
	Action string `json:"action"`
	ID     string `json:"id"`
}

// noise are actions that change nothing a list shows (and would fire constantly while a console is open).
var noise = []string{"attach", "detach", "resize", "top", "copy", "archive-path", "extract-to-dir", "export", "commit"}

// Register adds GET /events.
func Register(r fiber.Router, d dockerapi.Docker) {
	r.Get("/events", sdk.SSE(func(_ fiber.Ctx, s *sse.Stream) error { return forward(d, s) }))
}

func forward(d dockerapi.Docker, s *sse.Stream) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-s.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	res := d.Events(ctx, client.EventsListOptions{Filters: make(client.Filters).Add("type", "container", "image", "volume", "network")})
	// The response headers leave with the first write, and an idle stream would send none for a long time: this comment
	// opens it for the browser (and the proxy in front). Docker has registered the subscription by now, so nothing that
	// happens after the browser saw the stream open is missed.
	if err := s.Comment("subscribed"); err != nil {
		return nil
	}
	for {
		select {
		case <-s.Done():
			return nil
		case m := <-res.Messages:
			action, _, _ := strings.Cut(string(m.Action), ":")
			if strings.HasPrefix(action, "exec_") || slices.Contains(noise, action) {
				continue
			}
			if err := s.Event(sse.Event{Name: "docker", Data: Event{Type: string(m.Type), Action: action, ID: m.Actor.ID}}); err != nil {
				return nil
			}
		case err := <-res.Err:
			if ctx.Err() == nil {
				slog.Debug("docker events ended", "err", err) // the browser's EventSource connects again
			}
			return nil
		}
	}
}
