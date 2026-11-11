// Command edelweiss-ext-docker is the Docker extension of edelweiss: a Go backend that talks to the local Docker daemon
// and a SolidJS UI, in one static binary.
package main

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"

	"github.com/Pukka922/edelweiss-ext-docker/internal/api"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/images"
	"github.com/Pukka922/edelweiss-ext-docker/internal/registries"
	"github.com/Pukka922/edelweiss/sdk"
)

//go:embed manifest.json
var manifest []byte

// The version of a release: only the release build sets it, with -ldflags "-X main.version=<version>" (make release),
// and it is empty in every other build, which then reports the placeholder version of manifest.json.
var version string

// manifestWithVersion returns the manifest with its version field set to v. An empty v returns raw itself, so a build
// without a release version reports the placeholder of manifest.json.
func manifestWithVersion(raw []byte, v string) ([]byte, error) {
	if v == "" {
		return raw, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if fields == nil { // the JSON value null unmarshals without an error
		return nil, fmt.Errorf("manifest: not a JSON object")
	}
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("manifest: encode version %q: %w", v, err)
	}
	fields["version"] = encoded
	out, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	return out, nil
}

// The built UI (make build runs npm run build in ui/ first). ui/dist holds a committed .gitkeep, so the package
// compiles before the first UI build too.
//
//go:embed all:ui/dist
var uiFiles embed.FS

func main() {
	m, err := manifestWithVersion(manifest, version)
	if err != nil {
		panic(err)
	}
	ui, err := fs.Sub(uiFiles, "ui/dist")
	if err != nil {
		panic(err)
	}
	ext := sdk.New(sdk.Options{Manifest: m, UI: ui})
	docker, err := dockerapi.New(dockerapi.DefaultHost)
	if err != nil {
		panic(err)
	}
	store := registries.NewStore(registries.DefaultPath)
	api.Register(ext.App, api.Deps{Docker: docker, Registries: store, Pulls: images.NewPulls(docker, store)})
	ext.Run()
}
