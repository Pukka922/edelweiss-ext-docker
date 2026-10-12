// Command edelweiss-ext-docker is the Docker extension of edelweiss: a Go backend that talks to the local Docker daemon
// and a SolidJS UI, in one static binary.
package main

import (
	"embed"
	"io/fs"

	"github.com/Pukka922/edelweiss-ext-docker/internal/api"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/images"
	"github.com/Pukka922/edelweiss-ext-docker/internal/registries"
	"github.com/Pukka922/edelweiss/sdk"
)

//go:embed manifest.json
var manifest []byte

// The built UI (make build runs npm run build in ui/ first). ui/dist holds a committed .gitkeep, so the package
// compiles before the first UI build too.
//
//go:embed all:ui/dist
var uiFiles embed.FS

func main() {
	ui, err := fs.Sub(uiFiles, "ui/dist")
	if err != nil {
		panic(err)
	}
	ext := sdk.New(sdk.Options{Manifest: manifest, UI: ui})
	docker, err := dockerapi.New(dockerapi.DefaultHost)
	if err != nil {
		panic(err)
	}
	store := registries.NewStore(registries.DefaultPath)
	api.Register(ext.App, api.Deps{Docker: docker, Registries: store, Pulls: images.NewPulls(docker, store)})
	ext.Run()
}
