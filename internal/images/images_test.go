package images

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

var anna = sdk.User{Name: "anna", UID: 1000}

func testApp(t *testing.T, d dockerapi.Docker) (*fiber.App, *Pulls) {
	s := testStore(t)
	p := NewPulls(d, s)
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, d, p, s)
	return app, p
}

func TestList(t *testing.T) {
	f := &dockertest.Fake{
		ImageListFn: func(context.Context, client.ImageListOptions) (client.ImageListResult, error) {
			return client.ImageListResult{Items: []image.Summary{
				{ID: "sha256:old", RepoTags: []string{"<none>:<none>"}, Created: 100, Size: 5},
				{ID: "sha256:nginx", RepoTags: []string{"nginx:1.29", "nginx:latest"}, Created: 200, Size: 70},
			}}, nil
		},
		ContainerListFn: func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
			return client.ContainerListResult{Items: []container.Summary{{ID: "c1aaaaaaaaaaaaaaaa", Names: []string{"/web"}, ImageID: "sha256:nginx"}}}, nil
		},
	}
	app, _ := testApp(t, f)
	got := sdktest.DecodeJSON[[]Image](t, sdktest.Do(t, app, sdktest.Request("GET", "/images", nil, anna)))
	if len(got) != 2 || got[0].ID != "sha256:nginx" || len(got[0].Tags) != 2 || len(got[0].UsedBy) != 1 ||
		got[1].ID != "sha256:old" || got[1].Tags == nil || len(got[1].Tags) != 0 {
		t.Fatalf("images %+v (newest first, <none> dropped)", got)
	}
}

// The canonical image IDs of the tests: DELETE /images/:id accepts nothing else.
var (
	usedID  = "sha256:" + strings.Repeat("a", 64)
	freeID  = "sha256:" + strings.Repeat("b", 64)
	otherID = "sha256:" + strings.Repeat("e", 64)
)

// layer is the digest of a layer (or of a manifest or a config): no image ID.
func layer(n int) string { return fmt.Sprintf("sha256:%064d", n) }

// The containerd image store reports the image (its index), its manifests, configs and layers as deleted: one image.
func TestPruneCountsImagesOnly(t *testing.T) {
	f := &dockertest.Fake{
		ImageListFn: func(context.Context, client.ImageListOptions) (client.ImageListResult, error) {
			return client.ImageListResult{Items: []image.Summary{{ID: usedID}, {ID: freeID}}}, nil
		},
		ImagePruneFn: func(context.Context, client.ImagePruneOptions) (client.ImagePruneResult, error) {
			return client.ImagePruneResult{Report: image.PruneReport{
				ImagesDeleted: []image.DeleteResponse{
					{Untagged: "busybox:1.37"}, {Untagged: "busybox@" + freeID},
					{Deleted: freeID}, {Deleted: layer(1)}, {Deleted: layer(2)}, {Deleted: layer(3)}, {Deleted: layer(4)}, {Deleted: freeID},
				},
				SpaceReclaimed: 2 << 20,
			}}, nil
		},
	}
	app, _ := testApp(t, f)
	got := sdktest.DecodeJSON[struct {
		Deleted        int    `json:"deleted"`
		SpaceReclaimed uint64 `json:"spaceReclaimed"`
	}](t, sdktest.Do(t, app, sdktest.Request("POST", "/images/prune", nil, anna)))
	if got.Deleted != 1 || got.SpaceReclaimed != 2<<20 {
		t.Fatalf("prune %+v, want 1 image and Docker's space", got)
	}

	// the list before the prune fails: no prune
	pruned := false
	f.ImageListFn = func(context.Context, client.ImageListOptions) (client.ImageListResult, error) {
		return client.ImageListResult{}, errors.New("Error response from daemon: boom")
	}
	f.ImagePruneFn = func(context.Context, client.ImagePruneOptions) (client.ImagePruneResult, error) {
		pruned = true
		return client.ImagePruneResult{}, nil
	}
	if resp := sdktest.Do(t, app, sdktest.Request("POST", "/images/prune", nil, anna)); resp.StatusCode != 502 || pruned {
		t.Fatalf("list error: %d, pruned %v", resp.StatusCode, pruned)
	}
}

func TestPresentDeletePrune(t *testing.T) {
	var removed string
	var removeOpts client.ImageRemoveOptions
	f := &dockertest.Fake{
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "nginx:1.29" {
				return client.ImageInspectResult{}, nil
			}
			return client.ImageInspectResult{}, cerrdefs.ErrNotFound
		},
		ContainerListFn: func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
			return client.ContainerListResult{Items: []container.Summary{{ID: "c1aaaaaaaaaaaaaaaa", Names: []string{"/web"}, ImageID: usedID}}}, nil
		},
		ImageRemoveFn: func(_ context.Context, id string, o client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
			removed, removeOpts = strings.Clone(id), o // Fiber reuses the buffer of the strings it hands out
			return client.ImageRemoveResult{}, nil
		},
		ImageListFn: func(context.Context, client.ImageListOptions) (client.ImageListResult, error) {
			return client.ImageListResult{Items: []image.Summary{{ID: usedID}, {ID: freeID}, {ID: otherID}}}, nil
		},
		ImagePruneFn: func(_ context.Context, o client.ImagePruneOptions) (client.ImagePruneResult, error) {
			if !o.Filters["dangling"]["false"] {
				t.Errorf("prune must remove all unused images: %v", o.Filters)
			}
			// the classic image store reports the layers it deleted as well
			return client.ImagePruneResult{Report: image.PruneReport{
				ImagesDeleted: []image.DeleteResponse{
					{Untagged: "x:1"}, {Deleted: freeID}, {Deleted: layer(1)}, {Deleted: layer(2)},
					{Untagged: "y:1"}, {Deleted: otherID}, {Deleted: layer(3)},
				},
				SpaceReclaimed: 4096,
			}}, nil
		},
	}
	app, _ := testApp(t, f)
	present := func(ref string) bool {
		return sdktest.DecodeJSON[struct {
			Present bool `json:"present"`
		}](t, sdktest.Do(t, app, sdktest.Request("GET", "/images/present?ref="+ref, nil, anna))).Present
	}
	if !present("nginx:1.29") || present("nginx:9") {
		t.Fatal("present")
	}
	if resp := sdktest.Do(t, app, sdktest.Request("GET", "/images/present", nil, anna)); resp.StatusCode != 400 {
		t.Fatalf("present without ref: %d", resp.StatusCode)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/images/"+usedID, nil, anna)); resp.StatusCode != 409 || removed != "" {
		t.Fatalf("delete in use: %d %q", resp.StatusCode, removed)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/images/"+strings.Replace(usedID, ":", "%3A", 1), nil, anna)); resp.StatusCode != 409 || removed != "" {
		t.Fatalf("delete in use, encoded: %d %q", resp.StatusCode, removed)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/images/"+freeID, nil, anna)); resp.StatusCode != 204 || removed != freeID ||
		!removeOpts.Force || !removeOpts.PruneChildren {
		t.Fatalf("delete: %d %q %+v", resp.StatusCode, removed, removeOpts)
	}
	got := sdktest.DecodeJSON[struct {
		Deleted        int    `json:"deleted"`
		SpaceReclaimed uint64 `json:"spaceReclaimed"`
	}](t, sdktest.Do(t, app, sdktest.Request("POST", "/images/prune", nil, anna)))
	if got.Deleted != 2 || got.SpaceReclaimed != 4096 {
		t.Fatalf("prune %+v", got)
	}
}

// refused checks that the request is answered 400 invalid_request for the field.
func refused(t *testing.T, app *fiber.App, method, target, field string) {
	t.Helper()
	resp := sdktest.Do(t, app, sdktest.Request(method, target, nil, anna))
	body := sdktest.DecodeJSON[struct {
		Error struct {
			Code  string `json:"code"`
			Field string `json:"field"`
		} `json:"error"`
	}](t, resp)
	if resp.StatusCode != 400 || body.Error.Code != "invalid_request" || body.Error.Field != field {
		t.Errorf("%s %s: %d %+v, want 400 invalid_request for field %q", method, target, resp.StatusCode, body.Error, field)
	}
}

// Only a canonical image ID may be deleted: a tag, a short ID or a value the Docker client would clean into another
// endpoint (path.Join) could bypass the check for containers that use the image, or reach other routes of the daemon.
func TestDeleteAcceptsOnlyCanonicalIDs(t *testing.T) {
	var removed []string
	f := &dockertest.Fake{
		ContainerListFn: func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
			return client.ContainerListResult{}, nil
		},
		ImageRemoveFn: func(_ context.Context, id string, _ client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
			removed = append(removed, strings.Clone(id))
			return client.ImageRemoveResult{}, nil
		},
	}
	app, _ := testApp(t, f)
	hex := strings.Repeat("c", 64)
	for _, id := range []string{
		"nginx:1.29",                          // a tag
		"nginx",                               // a name
		"sha256:" + strings.Repeat("c", 12),   // a short ID
		strings.Repeat("c", 12),               // a short ID without the algorithm
		hex,                                   // the full ID without the algorithm
		"sha256:" + strings.ToUpper(hex),      // not lower case
		"sha256:" + hex + "%2Fjson",           // a suffix
		"..%2Fcontainers%2F" + hex,            // path traversal
		"%2E%2E%2Fcontainers%2F" + hex,        // path traversal, dots encoded
		"sha256%3A..%2F..%2Fcontainers%2Fabc", // path traversal behind the algorithm
		"sha256:" + hex + "%3Fforce=1",        // a query
	} {
		refused(t, app, "DELETE", "/images/"+id, "id")
	}
	if len(removed) != 0 {
		t.Fatalf("ImageRemove must not be called for these ids, was called with %q", removed)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/images/sha256:"+hex, nil, anna)); resp.StatusCode != 204 || len(removed) != 1 {
		t.Fatalf("a canonical id: %d %q", resp.StatusCode, removed)
	}
}

// GET /images/present passes the reference to the daemon in a URL path: refuse what the client would clean into
// another endpoint.
func TestPresentRefusesUnsafeRefs(t *testing.T) {
	var inspected []string
	f := &dockertest.Fake{ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
		inspected = append(inspected, strings.Clone(ref)) // Fiber reuses the buffer of the strings it hands out
		return client.ImageInspectResult{}, nil
	}}
	app, _ := testApp(t, f)
	for _, ref := range []string{
		"/etc/passwd",
		"../containers/abc",
		"nginx/../../containers/abc",
		"..",
		"nginx/..",
		"nginx?all=1",
		"nginx#frag",
	} {
		refused(t, app, "GET", "/images/present?ref="+url.QueryEscape(ref), "ref")
	}
	if len(inspected) != 0 {
		t.Fatalf("ImageInspect must not be called for these refs, was called with %q", inspected)
	}
	for _, ref := range []string{"nginx:1.29", "ghcr.io/octo/app:v1.2", "localhost:5000/test/busybox:1.37", "my..image", "nginx@sha256:" + strings.Repeat("d", 64)} {
		inspected = nil
		resp := sdktest.Do(t, app, sdktest.Request("GET", "/images/present?ref="+url.QueryEscape(ref), nil, anna))
		if resp.StatusCode != 200 || len(inspected) != 1 || inspected[0] != ref {
			t.Errorf("ref %q: %d, inspected %q", ref, resp.StatusCode, inspected)
		}
	}
}

func TestPullRoutes(t *testing.T) {
	fp := newFakePull()
	var pulled string
	f := &dockertest.Fake{ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
		pulled = ref
		return fp, nil
	}}
	app, pulls := testApp(t, f)
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/pulls", strings.NewReader(`{"registry":"","image":"busybox","tag":"1.37"}`), anna))
	started := sdktest.DecodeJSON[struct{ ID, Ref string }](t, resp)
	if resp.StatusCode != 202 || started.Ref != "busybox:1.37" || started.ID == "" {
		t.Fatalf("start: %d %+v", resp.StatusCode, started)
	}
	if bad := sdktest.Do(t, app, sdktest.Request("POST", "/pulls", strings.NewReader(`{"image":"Bad Name"}`), anna)); bad.StatusCode != 400 {
		t.Fatalf("bad image: %d", bad.StatusCode)
	}
	if unknown := sdktest.Do(t, app, sdktest.Request("GET", "/pulls/nope/events", nil, anna)); unknown.StatusCode != 404 {
		t.Fatalf("unknown pull: %d", unknown.StatusCode)
	}
	go func() {
		fp.msgs <- jsonstream.Message{ID: "aaaa", Status: "Downloading", Progress: &jsonstream.Progress{Current: 1, Total: 2}}
		close(fp.msgs)
	}()
	events := sdktest.Do(t, app, sdktest.Request("GET", "/pulls/"+started.ID+"/events", nil, anna))
	var names []string
	sc := bufio.NewScanner(events.Body)
	for sc.Scan() {
		if name, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
			names = append(names, name)
		}
	}
	if len(names) < 2 || names[0] != "progress" || names[len(names)-1] != "done" {
		t.Fatalf("events %v", names)
	}
	if pulled != "busybox:1.37" {
		t.Fatalf("pulled %q", pulled)
	}
	if p, ok := pulls.Get(started.ID); !ok || !waitDone(p) {
		t.Fatal("the pull must be finished and kept")
	}
}

func waitDone(p *Pull) bool { snap, _ := p.Watch(); return snap.Done }
