package api

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss-ext-docker/internal/images"
	"github.com/Pukka922/edelweiss-ext-docker/internal/registries"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

func statusApp(t *testing.T, f *dockertest.Fake) *fiber.App {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	store := registries.NewStore(filepath.Join(t.TempDir(), "registries.db"))
	Register(app, Deps{Docker: f, Registries: store, Pulls: images.NewPulls(f, store)})
	return app
}

func TestStatusOK(t *testing.T) {
	f := &dockertest.Fake{ServerVersionFn: func(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error) {
		return client.ServerVersionResult{Version: "29.8.2", APIVersion: "1.55", Os: "linux", Arch: "arm64"}, nil
	}}
	resp := sdktest.Do(t, statusApp(t, f), sdktest.Request("GET", "/status", nil, sdk.User{Name: "anna", Admin: true}))
	got := sdktest.DecodeJSON[Status](t, resp)
	want := Status{Docker: "ok", Version: "29.8.2", APIVersion: "1.55", OS: "linux", Arch: "arm64", Admin: true}
	if got != want {
		t.Fatalf("status %+v, want %+v", got, want)
	}
}

func TestStatusWithoutDocker(t *testing.T) {
	for name, err := range map[string]error{"unavailable": dockertest.Unavailable(t), "forbidden": dockertest.Forbidden(t)} {
		f := &dockertest.Fake{ServerVersionFn: func(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error) {
			return client.ServerVersionResult{}, err
		}}
		resp := sdktest.Do(t, statusApp(t, f), sdktest.Request("GET", "/status", nil, sdk.User{Name: "anna"}))
		got := sdktest.DecodeJSON[Status](t, resp)
		if resp.StatusCode != 200 || string(got.Docker) != name || got.Message == "" || got.Version != "" {
			t.Fatalf("%s: %d %+v", name, resp.StatusCode, got)
		}
	}
}

func TestStatusOtherError(t *testing.T) {
	f := &dockertest.Fake{ServerVersionFn: func(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error) {
		return client.ServerVersionResult{}, errors.New("Error response from daemon: confused")
	}}
	resp := sdktest.Do(t, statusApp(t, f), sdktest.Request("GET", "/status", nil, sdk.User{Name: "anna"}))
	if resp.StatusCode != 502 {
		t.Fatalf("status %d, want 502", resp.StatusCode)
	}
}

func TestStatusNamesTheHostsLimits(t *testing.T) {
	f := &dockertest.Fake{
		ServerVersionFn: func(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error) {
			return client.ServerVersionResult{Version: "29.9.0"}, nil
		},
		InfoFn: func(context.Context, client.InfoOptions) (client.SystemInfoResult, error) {
			return client.SystemInfoResult{Info: system.Info{NCPU: 4, MemTotal: 8 << 30}}, nil
		},
	}
	got := sdktest.DecodeJSON[Status](t, sdktest.Do(t, statusApp(t, f), sdktest.Request("GET", "/status", nil, sdk.User{Name: "anna"})))
	if got.CPUs != 4 || got.Memory != 8<<30 {
		t.Fatalf("status %+v", got)
	}
}

func TestStatusWithoutInfoStillAnswers(t *testing.T) {
	f := &dockertest.Fake{
		ServerVersionFn: func(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error) {
			return client.ServerVersionResult{Version: "29.9.0"}, nil
		},
		InfoFn: func(context.Context, client.InfoOptions) (client.SystemInfoResult, error) {
			return client.SystemInfoResult{}, errors.New("Error response from daemon: busy")
		},
	}
	resp := sdktest.Do(t, statusApp(t, f), sdktest.Request("GET", "/status", nil, sdk.User{Name: "anna"}))
	got := sdktest.DecodeJSON[Status](t, resp)
	if resp.StatusCode != 200 || got.Docker != "ok" || got.CPUs != 0 || got.Memory != 0 {
		t.Fatalf("%d %+v", resp.StatusCode, got)
	}
}
