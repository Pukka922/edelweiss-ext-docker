package containers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// editFake answers like the daemon for the container of inspected(); calls are recorded as "verb object …" with "old"
// for the container being edited and "new" for the one created, and every call named in fail fails. A call with a
// cancelled context fails, as it would at the daemon.
func editFake(fail ...string) (*dockertest.Fake, *[]string) {
	c, img := inspected()
	var calls []string
	name := func(id string) string {
		if id == c.ID {
			return "old"
		}
		return id
	}
	step := func(ctx context.Context, call string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		calls = append(calls, call)
		for _, f := range fail {
			if f == call {
				return errors.New("Error response from daemon: " + call + " failed")
			}
		}
		return nil
	}
	f := &dockertest.Fake{
		ContainerInspectFn: func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: c}, nil
		},
		ImageInspectFn: func(_ context.Context, ref string, _ ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref == "nginx:9.9" {
				return client.ImageInspectResult{}, cerrdefs.ErrNotFound
			}
			return client.ImageInspectResult{InspectResponse: *img}, nil
		},
		ContainerRenameFn: func(ctx context.Context, id string, o client.ContainerRenameOptions) (client.ContainerRenameResult, error) {
			return client.ContainerRenameResult{}, step(ctx, "rename "+name(id)+" "+o.NewName)
		},
		ContainerUpdateFn: func(ctx context.Context, id string, o client.ContainerUpdateOptions) (client.ContainerUpdateResult, error) {
			return client.ContainerUpdateResult{}, step(ctx, "update "+name(id)+" "+string(o.RestartPolicy.Name))
		},
		ContainerCreateFn: func(ctx context.Context, o client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
			if err := step(ctx, "create "+o.Name); err != nil {
				return client.ContainerCreateResult{}, err
			}
			return client.ContainerCreateResult{ID: "new"}, nil
		},
		NetworkConnectFn: func(ctx context.Context, network string, o client.NetworkConnectOptions) (client.NetworkConnectResult, error) {
			return client.NetworkConnectResult{}, step(ctx, "connect "+network+" "+name(o.Container))
		},
		ContainerStopFn: func(ctx context.Context, id string, _ client.ContainerStopOptions) (client.ContainerStopResult, error) {
			return client.ContainerStopResult{}, step(ctx, "stop "+name(id))
		},
		ContainerStartFn: func(ctx context.Context, id string, _ client.ContainerStartOptions) (client.ContainerStartResult, error) {
			return client.ContainerStartResult{}, step(ctx, "start "+name(id))
		},
		ContainerRemoveFn: func(ctx context.Context, id string, o client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
			return client.ContainerRemoveResult{}, step(ctx, fmt.Sprintf("remove %s force=%v volumes=%v", name(id), o.Force, o.RemoveVolumes))
		},
	}
	return f, &calls
}

// changed is the form of inspected() with one more variable: that needs a recreation.
func changed() Spec {
	c, img := inspected()
	s := SpecOf(c, img)
	s.Env = append(s.Env, EnvVar{Name: "DEBUG", Value: "1"})
	return s.Normalize()
}

// recreated are the calls of a recreation of the running container of inspected() that goes well.
func recreated() []string {
	return []string{"rename old web-edelweiss-old", "create web", "connect back new", "stop old", "start new", "remove old force=false volumes=false"}
}

// asAPIError is err as the answer of the extension, or nil.
func asAPIError(err error) *sdk.APIError {
	var apiErr *sdk.APIError
	if errors.As(err, &apiErr) {
		return apiErr
	}
	return nil
}

func toJSONUnit(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sdktestDo(t *testing.T, app *fiber.App, method, path, body string) *http.Response {
	return sdktest.Do(t, app, sdktest.Request(method, path, strings.NewReader(body), anna))
}

func TestEditDirect(t *testing.T) {
	f, calls := editFake()
	c, img := inspected()
	if c.Image != img.ID {
		t.Fatalf("the fixture must have the container run the image the ref names now: %q vs %q", c.Image, img.ID)
	}
	s := SpecOf(c, img)
	s.Name, s.Restart = "web2", Restart{Policy: "always"}
	res, err := New(f).Edit(context.Background(), "web", s.Normalize())
	if err != nil || res.Recreated || res.ID != c.ID {
		t.Fatalf("edit: %+v %v", res, err)
	}
	if want := "rename old web2,update old always"; strings.Join(*calls, ",") != want {
		t.Fatalf("calls %v, want %s", *calls, want)
	}
}

// "Image neu laden" pulls and then sends the form unchanged: when the pull moved the tag to another image the container
// must be recreated, or it keeps running the old image.
func TestEditRecreatesForAnotherImageBehindTheRef(t *testing.T) {
	c, img := inspected()
	form := SpecOf(c, img)
	// the ref nginx:1.29 now names the image with this id, or no image at all for ""
	refNames := func(f *dockertest.Fake, id string) {
		inner := f.ImageInspectFn
		f.ImageInspectFn = func(ctx context.Context, ref string, o ...client.ImageInspectOption) (client.ImageInspectResult, error) {
			if ref != "nginx:1.29" {
				return inner(ctx, ref, o...)
			}
			if id == "" {
				return client.ImageInspectResult{}, cerrdefs.ErrNotFound
			}
			res, err := inner(ctx, ref, o...)
			res.ID = id
			return res, err
		}
	}
	cases := []struct {
		name      string
		id        string // "": the ref names no image locally
		calls     []string
		recreated bool
	}{
		{"same image", img.ID, nil, false},
		{"another image", "sha256:nginx-new", recreated(), true},
		{"image gone: nothing to recreate from, nothing to do", "", nil, false},
	}
	for _, tc := range cases {
		f, calls := editFake()
		refNames(f, tc.id)
		res, err := New(f).Edit(context.Background(), "web", form)
		if err != nil || res.Recreated != tc.recreated || res.Warning != "" {
			t.Errorf("%s: %+v %v", tc.name, res, err)
		}
		if tc.recreated && res.ID != "new" || !tc.recreated && res.ID != c.ID {
			t.Errorf("%s: id %q", tc.name, res.ID)
		}
		if got, want := strings.Join(*calls, ","), strings.Join(tc.calls, ","); got != want {
			t.Errorf("%s: calls\n %s\nwant\n %s", tc.name, got, want)
		}
	}
}

// The order of the rows of the form is no change: a form whose ports, mounts and variables only come in another order is
// still edited directly (rename, restart policy), and with one real change it recreates.
func TestEditIgnoresTheOrderOfTheRows(t *testing.T) {
	c, img := inspected()
	c.Config.Env = append(c.Config.Env, "ZED=1", "ALPHA=2")
	c.HostConfig.PortBindings[network.MustParsePort("443/tcp")] = []network.PortBinding{{HostPort: "8443"}}
	cases := []struct {
		name      string
		change    func(*Spec)
		calls     string
		recreated bool
	}{
		{"reordered and renamed", func(s *Spec) { s.Name = "web2" }, "rename old web2", false},
		{"reordered, one more variable", func(s *Spec) { s.Env = append(s.Env, EnvVar{Name: "DEBUG", Value: "1"}) }, strings.Join(recreated(), ","), true},
	}
	for _, tc := range cases {
		f, calls := editFake()
		f.ContainerInspectFn = func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: c}, nil
		}
		form := SpecOf(c, img)
		if len(form.Ports) < 2 || len(form.Mounts) < 2 || len(form.Env) < 2 {
			t.Fatalf("the fixture needs two rows of each: %+v", form)
		}
		slices.Reverse(form.Ports)
		slices.Reverse(form.Mounts)
		slices.Reverse(form.Env)
		tc.change(&form)
		res, err := New(f).Edit(context.Background(), "web", form)
		if err != nil || res.Recreated != tc.recreated {
			t.Errorf("%s: %+v %v", tc.name, res, err)
		}
		if got := strings.Join(*calls, ","); got != tc.calls {
			t.Errorf("%s: calls\n %s\nwant\n %s", tc.name, got, tc.calls)
		}
	}
}

// A command that splits into the same words is no change, whatever the spacing and quoting; one that does not split is.
func TestEditComparesCommandsAsWords(t *testing.T) {
	cases := []struct {
		command   string
		recreated bool
		invalid   bool
	}{
		{"sleep 300", false, false},
		{"  sleep    300  ", false, false},
		{`'sleep' "300"`, false, false},
		{"sleep 301", true, false},
		{"sleep '300", false, true},
	}
	for _, tc := range cases {
		f, calls := editFake()
		inner := f.ContainerInspectFn
		f.ContainerInspectFn = func(ctx context.Context, id string, o client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			res, err := inner(ctx, id, o)
			res.Container.Config.Cmd = []string{"sleep", "300"} // not the image's command, so the form shows it
			return res, err
		}
		c, img := inspected()
		form := SpecOf(c, img)
		form.Command = tc.command
		res, err := New(f).Edit(context.Background(), "web", form)
		switch apiErr := asAPIError(err); {
		case tc.invalid && (apiErr == nil || apiErr.Code != "invalid_container_spec" || len(*calls) != 0):
			t.Errorf("command %q: error %v, calls %v, want invalid_container_spec and no call", tc.command, err, *calls)
		case !tc.invalid && err != nil:
			t.Errorf("command %q: %v", tc.command, err)
		case !tc.invalid && res.Recreated != tc.recreated:
			t.Errorf("command %q: recreated %v, calls %v", tc.command, res.Recreated, *calls)
		case !tc.recreated && len(*calls) != 0:
			t.Errorf("command %q: no change, but calls %v", tc.command, *calls)
		}
	}
}

// If the restart policy cannot be updated after the container was renamed, the rename is taken back, and the answer says
// whether that worked.
func TestEditDirectUndoesTheRename(t *testing.T) {
	cases := []struct {
		name    string
		rename  bool
		fail    []string
		calls   []string
		message string
	}{
		{"undone", true, []string{"update old always"},
			[]string{"rename old web2", "update old always", "rename old web"}, "the rename to web2 was undone"},
		{"undo fails", true, []string{"update old always", "rename old web"},
			[]string{"rename old web2", "update old always", "rename old web"}, "is now called web2 and could not be renamed back to web"},
		{"no rename, nothing to undo", false, []string{"update old always"},
			[]string{"update old always"}, ""},
	}
	for _, tc := range cases {
		f, calls := editFake(tc.fail...)
		c, img := inspected()
		s := SpecOf(c, img)
		s.Restart = Restart{Policy: "always"}
		if tc.rename {
			s.Name = "web2"
		}
		_, err := New(f).Edit(context.Background(), "web", s.Normalize())
		apiErr := asAPIError(err)
		if apiErr == nil || apiErr.Code != "docker_error" || !strings.Contains(apiErr.Message, "update old always failed") {
			t.Errorf("%s: error %v", tc.name, err)
			continue
		}
		if tc.message != "" && !strings.Contains(apiErr.Message, tc.message) {
			t.Errorf("%s: message %q does not say %q", tc.name, apiErr.Message, tc.message)
		}
		if tc.message == "" && strings.Contains(apiErr.Message, "rename") {
			t.Errorf("%s: message %q talks about a rename", tc.name, apiErr.Message)
		}
		if got, want := strings.Join(*calls, ","), strings.Join(tc.calls, ","); got != want {
			t.Errorf("%s: calls\n %s\nwant\n %s", tc.name, got, want)
		}
	}
}

func TestRecreate(t *testing.T) {
	ok := recreated()
	cases := []struct {
		fail      []string
		calls     []string
		code      string
		recreated bool
		warning   bool
	}{
		{nil, ok, "", true, false},
		{[]string{"rename old web-edelweiss-old"}, ok[:1], "docker_error", false, false},
		{[]string{"create web"}, append(ok[:2:2], "rename old web"), "docker_error", false, false},
		{[]string{"connect back new"}, append(ok[:3:3], "remove new force=true volumes=false", "rename old web"), "docker_error", false, false},
		// the stop may have worked although the call failed: the old container is started again
		{[]string{"stop old"}, append(ok[:4:4], "remove new force=true volumes=false", "rename old web", "start old"), "docker_error", false, false},
		{[]string{"start new"}, append(ok[:5:5], "remove new force=true volumes=false", "rename old web", "start old"), "docker_error", false, false},
		{[]string{"remove old force=false volumes=false"}, ok, "", true, true},
		{[]string{"start new", "rename old web"}, append(ok[:5:5], "remove new force=true volumes=false", "rename old web", "start old"), "recreate_failed", false, false},
	}
	for _, tc := range cases {
		f, calls := editFake(tc.fail...)
		res, err := New(f).Edit(context.Background(), "web", changed())
		if got := strings.Join(*calls, ","); got != strings.Join(tc.calls, ",") {
			t.Errorf("fail %v: calls\n %s\nwant\n %s", tc.fail, got, strings.Join(tc.calls, ","))
		}
		var apiErr *sdk.APIError
		switch {
		case tc.code == "" && err != nil:
			t.Errorf("fail %v: %v", tc.fail, err)
		case tc.code != "" && (!errors.As(err, &apiErr) || apiErr.Code != tc.code):
			t.Errorf("fail %v: error %v, want %s", tc.fail, err, tc.code)
		case tc.code == "" && (res.Recreated != tc.recreated || (res.Warning != "") != tc.warning || res.ID != "new"):
			t.Errorf("fail %v: result %+v", tc.fail, res)
		}
		if tc.code == "recreate_failed" && !strings.Contains(err.Error(), "web-edelweiss-old") {
			t.Errorf("the message must say where the old container is: %v", err)
		}
	}
}

// When the rollback fails too, the message says where the old container really is (under its own name if it was renamed
// back, else under the parked one) and which new container is left over.
func TestRecreateFailureMessages(t *testing.T) {
	removeNew := "remove new force=true volumes=false"
	cases := []struct {
		name     string
		rename   string // the name the form gives the container ("" keeps it)
		fail     []string
		calls    []string
		contains []string
		omits    []string
	}{
		{"new container stays, old one renamed back", "web2", []string{"start new", removeNew},
			[]string{"rename old web-edelweiss-old", "create web2", "connect back new", "stop old", "start new", removeNew, "rename old web", "start old"},
			[]string{"removing the new container", "(the old container is called web; the new container web2 (new) is still there)"},
			[]string{"web-edelweiss-old"}},
		{"new container stays and keeps the name: old one stays parked", "", []string{"start new", removeNew, "rename old web"},
			[]string{"rename old web-edelweiss-old", "create web", "connect back new", "stop old", "start new", removeNew, "rename old web", "start old"},
			[]string{"renaming web-edelweiss-old back to web", "(the old container is called web-edelweiss-old; the new container web (new) is still there)"},
			nil},
		{"old container cannot be started again", "", []string{"start new", "start old"},
			[]string{"rename old web-edelweiss-old", "create web", "connect back new", "stop old", "start new", removeNew, "rename old web", "start old"},
			[]string{"starting the old container again", "(the old container is called web)"},
			[]string{"web-edelweiss-old", "still there"}},
	}
	for _, tc := range cases {
		f, calls := editFake(tc.fail...)
		s := changed()
		if tc.rename != "" {
			s.Name = tc.rename
		}
		_, err := New(f).Edit(context.Background(), "web", s)
		apiErr := asAPIError(err)
		if apiErr == nil || apiErr.Code != "recreate_failed" {
			t.Errorf("%s: error %v", tc.name, err)
			continue
		}
		if got, want := strings.Join(*calls, ","), strings.Join(tc.calls, ","); got != want {
			t.Errorf("%s: calls\n %s\nwant\n %s", tc.name, got, want)
		}
		for _, want := range tc.contains {
			if !strings.Contains(apiErr.Message, want) {
				t.Errorf("%s: message %q does not contain %q", tc.name, apiErr.Message, want)
			}
		}
		for _, not := range tc.omits {
			if strings.Contains(apiErr.Message, not) {
				t.Errorf("%s: message %q must not contain %q", tc.name, apiErr.Message, not)
			}
		}
	}
}

// A container that does not run is replaced without stopping anything and the new one is not started; the old one is
// removed.
func TestRecreateOfAStoppedContainer(t *testing.T) {
	f, calls := editFake()
	inner := f.ContainerInspectFn
	f.ContainerInspectFn = func(ctx context.Context, id string, o client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		res, err := inner(ctx, id, o)
		res.Container.State = &container.State{Running: false, Status: container.StateExited}
		return res, err
	}
	res, err := New(f).Edit(context.Background(), "web", changed())
	if err != nil || !res.Recreated || res.ID != "new" || res.Warning != "" {
		t.Fatalf("edit: %+v %v", res, err)
	}
	want := []string{"rename old web-edelweiss-old", "create web", "connect back new", "remove old force=false volumes=false"}
	if got := strings.Join(*calls, ","); got != strings.Join(want, ",") {
		t.Fatalf("calls\n %s\nwant\n %s", got, strings.Join(want, ","))
	}
}

// The name for parking is taken (an earlier recreation left a container behind): Docker answers 409, the edit stops there and
// nothing has changed.
func TestRecreateStopsAtAParkingConflict(t *testing.T) {
	f, calls := editFake()
	f.ContainerRenameFn = func(_ context.Context, _ string, o client.ContainerRenameOptions) (client.ContainerRenameResult, error) {
		*calls = append(*calls, "rename old "+o.NewName)
		return client.ContainerRenameResult{}, fmt.Errorf("Error response from daemon: Conflict. The container name %q is already in use: %w",
			"/"+o.NewName, cerrdefs.ErrConflict)
	}
	_, err := New(f).Edit(context.Background(), "web", changed())
	if apiErr := asAPIError(err); apiErr == nil || apiErr.Code != "conflict" || apiErr.Status != 409 {
		t.Fatalf("error %v, want 409 conflict", err)
	}
	if got := strings.Join(*calls, ","); got != "rename old web-edelweiss-old" {
		t.Fatalf("calls %s, want the rename alone", got)
	}
}

// A recreation and its rollback are not cut short by a caller that gives up: Edit does not depend on the route for that.
func TestEditIgnoresACancelledContext(t *testing.T) {
	f, calls := editFake()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res, err := New(f).Edit(ctx, "web", changed())
	if err != nil || !res.Recreated {
		t.Fatalf("edit: %+v %v", res, err)
	}
	if got := strings.Join(*calls, ","); got != strings.Join(recreated(), ",") {
		t.Fatalf("calls %s", got)
	}
}

// Docker removes a container with --rm (AutoRemove) when it stops: the parked old container would be gone after the stop,
// with nothing left to roll back to. Its recreation is refused; a direct edit still goes to Docker, whose own answer (it
// refuses restart policies for such a container) passes through.
func TestEditRefusesToRecreateAnAutoRemoveContainer(t *testing.T) {
	autoRemove := func(f *dockertest.Fake) {
		inner := f.ContainerInspectFn
		f.ContainerInspectFn = func(ctx context.Context, id string, o client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			res, err := inner(ctx, id, o)
			res.Container.HostConfig.AutoRemove = true
			return res, err
		}
	}
	f, calls := editFake()
	autoRemove(f)
	_, err := New(f).Edit(context.Background(), "web", changed())
	if apiErr := asAPIError(err); apiErr == nil || apiErr.Status != 409 || apiErr.Code != "conflict" || !strings.Contains(apiErr.Message, "--rm") {
		t.Fatalf("recreate: error %v, want 409 conflict", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls %v, want none", *calls)
	}

	c, img := inspected()
	f, calls = editFake()
	autoRemove(f)
	s := SpecOf(c, img)
	s.Name = "web2"
	if res, err := New(f).Edit(context.Background(), "web", s.Normalize()); err != nil || res.Recreated || strings.Join(*calls, ",") != "rename old web2" {
		t.Fatalf("rename: %+v %v, calls %v", res, err, *calls)
	}

	f, _ = editFake()
	autoRemove(f)
	f.ContainerUpdateFn = func(context.Context, string, client.ContainerUpdateOptions) (client.ContainerUpdateResult, error) {
		return client.ContainerUpdateResult{}, errors.New("Error response from daemon: Cannot update container web: Restart policy cannot be updated because AutoRemove is enabled for the container")
	}
	s = SpecOf(c, img)
	s.Restart = Restart{Policy: "always"}
	_, err = New(f).Edit(context.Background(), "web", s.Normalize())
	if apiErr := asAPIError(err); apiErr == nil || !strings.Contains(apiErr.Message, "AutoRemove is enabled") {
		t.Fatalf("restart policy: error %v, want Docker's answer", err)
	}
}

func TestRecreateNeedsTheImage(t *testing.T) {
	f, calls := editFake()
	s := changed()
	s.Image = "nginx:9.9"
	_, err := New(f).Edit(context.Background(), "web", s)
	var apiErr *sdk.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "image_not_found" || len(*calls) != 0 {
		t.Fatalf("error %v, calls %v", err, *calls)
	}
}

func TestEditRoute(t *testing.T) {
	f, _ := editFake()
	body := toJSONUnit(t, changed())
	resp := sdktestDo(t, testApp(f), "PUT", "/containers/web", body)
	if resp.StatusCode != 200 || !strings.Contains(readAll(t, resp), `"recreated":true`) {
		t.Fatalf("PUT: %d", resp.StatusCode)
	}
	bad := strings.Replace(body, `"name":"web"`, `"name":"w b"`, 1)
	resp = sdktestDo(t, testApp(f), "PUT", "/containers/web", bad)
	if raw := readAll(t, resp); resp.StatusCode != 400 || !strings.Contains(raw, "invalid_container_spec") || !strings.Contains(raw, `"field":"name"`) {
		t.Fatalf("invalid: %d %s", resp.StatusCode, raw)
	}
}

// The :id of PUT goes through containerID like that of every container route: an unsafe one answers 400 invalid_request
// (field id) before the body is looked at and without any call to Docker.
func TestEditRouteRefusesUnsafeIds(t *testing.T) {
	f, calls := editFake()
	inspects := 0
	inner := f.ContainerInspectFn
	f.ContainerInspectFn = func(ctx context.Context, id string, o client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		inspects++
		return inner(ctx, id, o)
	}
	app := testApp(f)
	body := toJSONUnit(t, changed())
	for _, id := range []string{".", "..", "..%2Fimages%2Fx", ".hidden", "-web", "web%2F..", "we%20b"} {
		resp := sdktestDo(t, app, "PUT", "/containers/"+id, body)
		if raw := readAll(t, resp); resp.StatusCode != 400 || !strings.Contains(raw, "invalid_request") || !strings.Contains(raw, `"field":"id"`) {
			t.Errorf("PUT /containers/%s: %d %s", id, resp.StatusCode, raw)
		}
	}
	if inspects != 0 || len(*calls) != 0 {
		t.Fatalf("Docker must not be called for these ids: %d inspects, calls %q", inspects, *calls)
	}
}
