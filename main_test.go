package main

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/Pukka922/edelweiss/sdk"
)

// sdk.New panics on an invalid manifest, so building the extension proves the embedded manifest.json is valid.
func TestManifest(t *testing.T) {
	ext := sdk.New(sdk.Options{Manifest: manifest})
	m := ext.Manifest
	if m.ID != "docker" || m.RequiresAdmin || m.Menu.Section != "virtualization" {
		t.Fatalf("manifest %+v", m)
	}
	if !slices.Equal(m.RequiredAPK(), []string{"docker"}) {
		t.Fatalf("requires.apk = %v, want [docker]", m.RequiredAPK())
	}
	if m.Version != "0.0.0-dev" {
		t.Fatalf("manifest version %q, want the placeholder 0.0.0-dev", m.Version)
	}
}

// Without a release version the manifest stays as it is.
func TestManifestWithoutVersion(t *testing.T) {
	got, err := manifestWithVersion(manifest, "")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, manifest) {
		t.Fatalf("manifestWithVersion(manifest, \"\") = %q, want the manifest unchanged", got)
	}
}

// A release version replaces the version field and nothing else: the parsed manifest equals the one of manifest.json
// with its version set, and so do the JSON objects (which also holds for fields the SDK does not know).
func TestManifestWithVersion(t *testing.T) {
	const v = "0.2.0-rc.1"
	raw, err := manifestWithVersion(manifest, v)
	if err != nil {
		t.Fatal(err)
	}
	got := sdk.New(sdk.Options{Manifest: raw}).Manifest
	if got.Version != v {
		t.Fatalf("manifest version %q, want %s", got.Version, v)
	}
	want := sdk.New(sdk.Options{Manifest: manifest}).Manifest
	want.Version = v
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("manifest = %+v, want the manifest.json one with version %s: %+v", got, v, want)
	}
	var gotFields, wantFields map[string]any
	if err := json.Unmarshal(raw, &gotFields); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(manifest, &wantFields); err != nil {
		t.Fatal(err)
	}
	wantFields["version"] = v
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("manifest fields = %v, want %v", gotFields, wantFields)
	}
}

func TestManifestWithVersionInvalidJSON(t *testing.T) {
	for _, raw := range []string{"{", "[]", "null"} {
		if got, err := manifestWithVersion([]byte(raw), "0.2.0"); err == nil {
			t.Fatalf("manifestWithVersion(%q) returned %q and no error", raw, got)
		}
	}
}
