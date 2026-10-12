package main

import (
	"slices"
	"testing"

	"github.com/Pukka922/edelweiss/sdk"
)

// sdk.New panics on an invalid manifest, so building the extension proves the embedded manifest.json is valid.
func TestManifest(t *testing.T) {
	ext := sdk.New(sdk.Options{Manifest: manifest})
	m := ext.Manifest
	if m.ID != "docker" || m.RequiresAdmin || m.Menu.Section != "containers" {
		t.Fatalf("manifest %+v", m)
	}
	if !slices.Equal(m.RequiredAPK(), []string{"docker"}) {
		t.Fatalf("requires.apk = %v, want [docker]", m.RequiredAPK())
	}
}
