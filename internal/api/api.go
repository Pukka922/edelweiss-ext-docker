// Package api wires the extension's HTTP routes; edelweissd forwards /ext/docker/api/* to them.
package api

import (
	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-docker/internal/console"
	"github.com/Pukka922/edelweiss-ext-docker/internal/containers"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/events"
	"github.com/Pukka922/edelweiss-ext-docker/internal/images"
	"github.com/Pukka922/edelweiss-ext-docker/internal/networks"
	"github.com/Pukka922/edelweiss-ext-docker/internal/registries"
	"github.com/Pukka922/edelweiss-ext-docker/internal/volumes"
)

// Deps are what the routes work with.
type Deps struct {
	Docker     dockerapi.Docker
	Registries *registries.Store
	Pulls      *images.Pulls
}

// Register adds all routes to r.
func Register(r fiber.Router, d Deps) {
	r.Get("/status", status(d.Docker))
	registries.Register(r, d.Registries, d.Docker)
	images.Register(r, d.Docker, d.Pulls, d.Registries)
	volumes.Register(r, d.Docker)
	networks.Register(r, d.Docker)
	containers.Register(r, containers.New(d.Docker))
	console.Register(r, d.Docker)
	events.Register(r, d.Docker)
}
