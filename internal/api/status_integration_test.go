//go:build integration

package api

import (
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss-ext-docker/internal/images"
	"github.com/Pukka922/edelweiss-ext-docker/internal/registries"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

func TestStatusAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	store := registries.NewStore(filepath.Join(t.TempDir(), "registries.db"))
	Register(app, Deps{Docker: cli, Registries: store, Pulls: images.NewPulls(cli, store)})
	got := sdktest.DecodeJSON[Status](t, sdktest.Do(t, app, sdktest.Request("GET", "/status", nil, sdk.User{Name: "anna"})))
	if got.Docker != "ok" || got.Version == "" || got.APIVersion == "" {
		t.Fatalf("status %+v", got)
	}
}
