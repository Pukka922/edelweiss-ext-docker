package containers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// updateWarnings are the warnings the fake's ContainerUpdate answers with.
var updateWarnings []string

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
			call := "update " + name(id)
			if o.RestartPolicy != nil {
				call += " " + string(o.RestartPolicy.Name)
			}
			if o.Resources != nil {
				call += fmt.Sprintf(" memory=%d swap=%d nano=%d", o.Resources.Memory, o.Resources.MemorySwap, o.Resources.NanoCPUs)
			}
			return client.ContainerUpdateResult{Warnings: updateWarnings}, step(ctx, call)
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

// carrying makes the container that f answers for carry the given labels (inspected() has app=web, and maintainer=NGINX
// of its image) and returns it.
func carrying(f *dockertest.Fake, labels map[string]string) container.InspectResponse {
	c, _ := inspected()
	c.Config.Labels = labels
	f.ContainerInspectFn = func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{Container: c}, nil
	}
	return c
}

// recordCreates makes f remember the labels of every container it is asked to create (as copies).
func recordCreates(f *dockertest.Fake) *[]map[string]string {
	var got []map[string]string
	inner := f.ContainerCreateFn
	f.ContainerCreateFn = func(ctx context.Context, o client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
		got = append(got, maps.Clone(o.Config.Labels))
		return inner(ctx, o)
	}
	return &got
}

// Docker cannot change the labels of a container: a change of the labels recreates it, with the labels of the form and
// Docker's own, which the form does not show.
func TestEditRecreatesForALabelChange(t *testing.T) {
	const project = "com.docker.compose.project"
	cases := []struct {
		name   string
		change func(*Spec) // the form shows app=web and tier=front
		want   map[string]string
	}{
		{"a label added", func(s *Spec) { s.Labels = append(s.Labels, Label{Key: "stage", Value: "prod"}) },
			map[string]string{"app": "web", "tier": "front", "stage": "prod", project: "shop"}},
		{"a label removed", func(s *Spec) { s.Labels = s.Labels[:1] }, map[string]string{"app": "web", project: "shop"}},
		{"a value changed", func(s *Spec) { s.Labels[0].Value = "api" }, map[string]string{"app": "api", "tier": "front", project: "shop"}},
		{"a key renamed", func(s *Spec) { s.Labels[1].Key = "layer" }, map[string]string{"app": "web", "layer": "front", project: "shop"}},
		{"a value emptied", func(s *Spec) { s.Labels[1].Value = "" }, map[string]string{"app": "web", "tier": "", project: "shop"}},
		{"all removed, as an empty list", func(s *Spec) { s.Labels = []Label{} }, map[string]string{project: "shop"}},
		{"all removed, as no list", func(s *Spec) { s.Labels = nil }, map[string]string{project: "shop"}},
	}
	for _, tc := range cases {
		f, calls := editFake()
		creates := recordCreates(f)
		c := carrying(f, map[string]string{"maintainer": "NGINX", "app": "web", "tier": "front", project: "shop"})
		_, img := inspected()
		form := SpecOf(c, img)
		if !slices.Equal(form.Labels, []Label{{Key: "app", Value: "web"}, {Key: "tier", Value: "front"}}) {
			t.Fatalf("%s: the form shows %+v", tc.name, form.Labels)
		}
		tc.change(&form)
		res, err := New(f).Edit(context.Background(), "web", form)
		if err != nil || !res.Recreated || res.ID != "new" {
			t.Errorf("%s: %+v %v", tc.name, res, err)
		}
		if !slices.Equal(*calls, recreated()) {
			t.Errorf("%s: calls %v", tc.name, *calls)
		}
		if len(*creates) != 1 || !maps.Equal((*creates)[0], tc.want) {
			t.Errorf("%s: the new container gets the labels %q, want %q", tc.name, *creates, tc.want)
		}
	}
}

// A change of the labels alongside name, restart policy or limits is not an edit by update: the container is recreated
// (under the new name, with the new policy and limits), and nothing is updated in place.
func TestEditWithALabelChangeAndDirectChangesRecreates(t *testing.T) {
	f, calls := editFake()
	creates := recordCreates(f)
	c := carrying(f, map[string]string{"app": "web"})
	_, img := inspected()
	form := SpecOf(c, img)
	form.Name, form.Restart, form.Resources = "web2", Restart{Policy: "always"}, Resources{Memory: 512 << 20}
	form.Labels = append(form.Labels, Label{Key: "tier", Value: "front"})
	res, err := New(f).Edit(context.Background(), "web", form)
	if err != nil || !res.Recreated {
		t.Fatalf("%+v %v", res, err)
	}
	want := []string{"rename old web-edelweiss-old", "create web2", "connect back new", "stop old", "start new", "remove old force=false volumes=false"}
	if !slices.Equal(*calls, want) {
		t.Fatalf("calls\n %v\nwant\n %v", *calls, want)
	}
	if len(*creates) != 1 || !maps.Equal((*creates)[0], map[string]string{"app": "web", "tier": "front"}) {
		t.Fatalf("labels %q", *creates)
	}
}

// The order of the labels is no change, and neither is a missing list for none; Docker's own labels, which the form does
// not show, are no change either. So these edits stay direct, and without any other change they ask Docker for nothing.
func TestEditIgnoresTheOrderOfTheLabelsAndDockersOwn(t *testing.T) {
	const compose = "com.docker.compose.project"
	own := map[string]string{"maintainer": "NGINX", "zone": "1", "app": "web", "Tier": "front", compose: "shop"}
	cases := []struct {
		name   string
		labels map[string]string // of the container
		change func(*Spec)
		calls  string
	}{
		{"reordered", own, func(s *Spec) { slices.Reverse(s.Labels) }, ""},
		{"reordered and renamed", own, func(s *Spec) { slices.Reverse(s.Labels); s.Name = "web2" }, "rename old web2"},
		{"reordered and another restart policy", own, func(s *Spec) { slices.Reverse(s.Labels); s.Restart = Restart{Policy: "always"} },
			"update old always"},
		{"a blank around a key", own, func(s *Spec) { s.Labels[0].Key = "  " + s.Labels[0].Key + " " }, ""},
		{"Docker's own labels and a rename", map[string]string{compose: "shop", "com.docker.compose.service": "web"},
			func(s *Spec) { s.Name = "web2" }, "rename old web2"},
		{"only the image's labels, none in the form", map[string]string{"maintainer": "NGINX"},
			func(s *Spec) { s.Labels = nil; s.Name = "web2" }, "rename old web2"},
		{"no labels at all, an empty list", nil, func(s *Spec) { s.Labels = []Label{}; s.Name = "web2" }, "rename old web2"},
		{"no labels at all, no list", nil, func(s *Spec) { s.Labels = nil }, ""},
	}
	for _, tc := range cases {
		f, calls := editFake()
		c := carrying(f, tc.labels)
		_, img := inspected()
		form := SpecOf(c, img)
		tc.change(&form)
		res, err := New(f).Edit(context.Background(), "web", form)
		if err != nil || res.Recreated || res.ID != c.ID {
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

// recordUpdates makes f remember the options of every ContainerUpdate (as copies), besides what the fake does with them.
func recordUpdates(f *dockertest.Fake) *[]client.ContainerUpdateOptions {
	var got []client.ContainerUpdateOptions
	inner := f.ContainerUpdateFn
	f.ContainerUpdateFn = func(ctx context.Context, id string, o client.ContainerUpdateOptions) (client.ContainerUpdateResult, error) {
		var cp client.ContainerUpdateOptions
		if o.Resources != nil {
			r := *o.Resources
			cp.Resources = &r
		}
		if o.RestartPolicy != nil {
			p := *o.RestartPolicy
			cp.RestartPolicy = &p
		}
		got = append(got, cp)
		return inner(ctx, id, o)
	}
	return &got
}

// Changing one limit sends the other as it is, with the swap Docker's rules give: the update carries both limits, and
// nothing but them (no restart policy, which did not change). Docker reads a 0 in an update as "unchanged", but the edit
// does not rely on that.
func TestEditOfOneLimitSendsTheOtherAsItIs(t *testing.T) {
	const half = 500_000_000 // 0.5 CPUs
	cases := []struct {
		name     string
		haveSwap int64 // MemorySwap of the container (memory 256 MiB, 0.5 CPUs)
		form     Resources
		want     container.Resources
	}{
		{"memory only: the CPU limit stays", 0, Resources{Memory: 512 << 20, CPUs: 0.5},
			container.Resources{Memory: 512 << 20, MemorySwap: 1024 << 20, NanoCPUs: half}},
		{"CPUs only: the memory limit stays, swap as Docker reports its default", 512 << 20, Resources{Memory: 256 << 20, CPUs: 1},
			container.Resources{Memory: 256 << 20, MemorySwap: 512 << 20, NanoCPUs: 1_000_000_000}},
		{"CPUs only: no swap limit reported", 0, Resources{Memory: 256 << 20, CPUs: 1},
			container.Resources{Memory: 256 << 20, MemorySwap: 512 << 20, NanoCPUs: 1_000_000_000}},
		{"CPUs only: a custom swap limit stays", 300 << 20, Resources{Memory: 256 << 20, CPUs: 1},
			container.Resources{Memory: 256 << 20, MemorySwap: 300 << 20, NanoCPUs: 1_000_000_000}},
		{"memory only: a custom swap limit above the new memory stays", 300 << 20, Resources{Memory: 280 << 20, CPUs: 0.5},
			container.Resources{Memory: 280 << 20, MemorySwap: 300 << 20, NanoCPUs: half}},
		{"CPUs only: unlimited swap stays", -1, Resources{Memory: 256 << 20, CPUs: 1},
			container.Resources{Memory: 256 << 20, MemorySwap: -1, NanoCPUs: 1_000_000_000}},
		{"memory only: unlimited swap stays", -1, Resources{Memory: 512 << 20, CPUs: 0.5},
			container.Resources{Memory: 512 << 20, MemorySwap: -1, NanoCPUs: half}},
	}
	for _, tc := range cases {
		f, calls := editFake()
		updates := recordUpdates(f)
		c, img := inspected()
		c.HostConfig.NanoCPUs, c.HostConfig.MemorySwap = half, tc.haveSwap
		f.ContainerInspectFn = func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: c}, nil
		}
		s := SpecOf(c, img)
		s.Resources = tc.form
		res, err := New(f).Edit(context.Background(), "web", s.Normalize())
		if err != nil || res.Recreated || res.ID != c.ID {
			t.Errorf("%s: %+v %v", tc.name, res, err)
			continue
		}
		if len(*calls) != 1 || len(*updates) != 1 {
			t.Errorf("%s: calls %v, want the one update", tc.name, *calls)
			continue
		}
		if u := (*updates)[0]; u.RestartPolicy != nil || !reflect.DeepEqual(u.Resources, &tc.want) {
			sent := "no resources"
			if u.Resources != nil {
				sent = fmt.Sprintf("memory=%d swap=%d nano=%d", u.Resources.Memory, u.Resources.MemorySwap, u.Resources.NanoCPUs)
			}
			t.Errorf("%s: update sent %s and restart policy %+v, want memory=%d swap=%d nano=%d and no policy", tc.name, sent, u.RestartPolicy,
				tc.want.Memory, tc.want.MemorySwap, tc.want.NanoCPUs)
		}
	}
}

func TestEditChangesTheLimitsDirectly(t *testing.T) {
	f, calls := editFake()
	c, img := inspected() // Memory 256 MiB, MemorySwap 0
	s := SpecOf(c, img)
	s.Resources = Resources{Memory: 512 << 20, CPUs: 1.5}
	updateWarnings = []string{"Your kernel does not support swap limit capabilities"}
	t.Cleanup(func() { updateWarnings = nil })
	res, err := New(f).Edit(context.Background(), "web", s.Normalize())
	if err != nil || res.Recreated || res.ID != c.ID || len(res.Warnings) != 1 {
		t.Fatalf("edit: %+v %v", res, err)
	}
	if want := fmt.Sprintf("update old memory=%d swap=%d nano=1500000000", 512<<20, 1024<<20); strings.Join(*calls, ",") != want {
		t.Fatalf("calls %v, want %s", *calls, want)
	}
}

func TestEditRecreatesToRemoveALimit(t *testing.T) {
	for name, r := range map[string]Resources{"memory": {Memory: 0, CPUs: 1}, "cpus": {Memory: 256 << 20, CPUs: 0}} {
		f, calls := editFake()
		c, img := inspected()
		c.HostConfig.NanoCPUs = 1_000_000_000
		f.ContainerInspectFn = func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: c}, nil
		}
		s := SpecOf(c, img)
		s.Resources = r
		res, err := New(f).Edit(context.Background(), "web", s.Normalize())
		if err != nil || !res.Recreated {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
		if !slices.Equal(*calls, recreated()) {
			t.Fatalf("%s: calls %v", name, *calls)
		}
	}
}

// The limits are compared as whole units (Docker's NanoCPUs), never as floats: 0.1+0.2 is 0.30000000000000004 as a float,
// yet the same limit as 0.3. It is no change, so it neither recreates nor asks Docker to update anything.
func TestEditComparesCPULimitsAsWholeNanoCPUs(t *testing.T) {
	a, b := 0.1, 0.2
	sum := a + b
	if sum == 0.3 {
		t.Fatal("the test needs a sum that is not 0.3 as a float")
	}
	cases := []struct {
		name  string
		name2 string // the new name of the container ("" keeps it)
		cpus  float64
		calls string
	}{
		{"the same limit as a float sum", "", sum, ""},
		{"the same limit, plain", "", 0.3, ""},
		{"the same limit and a rename: the rename alone", "web2", sum, "rename old web2"},
	}
	for _, tc := range cases {
		f, calls := editFake()
		c, img := inspected()
		c.HostConfig.NanoCPUs = 300_000_000
		f.ContainerInspectFn = func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: c}, nil
		}
		s := SpecOf(c, img)
		if s.Resources.CPUs != 0.3 {
			t.Fatalf("%s: the container shows %v CPUs, want 0.3", tc.name, s.Resources.CPUs)
		}
		s.Resources.CPUs = tc.cpus
		if tc.name2 != "" {
			s.Name = tc.name2
		}
		res, err := New(f).Edit(context.Background(), "web", s.Normalize())
		if err != nil || res.Recreated || res.ID != c.ID {
			t.Errorf("%s: %+v %v", tc.name, res, err)
		}
		if got := strings.Join(*calls, ","); got != tc.calls {
			t.Errorf("%s: calls %q, want %q", tc.name, got, tc.calls)
		}
	}
}

func TestEditRefusesALimitAboveACustomSwapBeforeChangingAnything(t *testing.T) {
	f, calls := editFake()
	c, img := inspected()
	c.HostConfig.MemorySwap = 300 << 20
	f.ContainerInspectFn = func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{Container: c}, nil
	}
	s := SpecOf(c, img)
	s.Name, s.Resources.Memory = "web2", 1<<30
	_, err := New(f).Edit(context.Background(), "web", s.Normalize())
	if apiErr := asAPIError(err); apiErr == nil || apiErr.Status != 400 || apiErr.Code != "invalid_container_spec" {
		t.Fatalf("err %v", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("calls %v, want none", *calls)
	}
}

func TestEditDirectUndoesTheRenameWhenDockerRefusesTheLimits(t *testing.T) {
	f, calls := editFake(fmt.Sprintf("update old memory=%d swap=%d nano=500000000", 256<<20, 512<<20))
	c, img := inspected()
	s := SpecOf(c, img)
	s.Name, s.Resources.CPUs = "web2", 0.5
	_, err := New(f).Edit(context.Background(), "web", s.Normalize())
	apiErr := asAPIError(err)
	if apiErr == nil || !strings.Contains(apiErr.Message, "failed") || !strings.Contains(apiErr.Message, "the rename to web2 was undone") {
		t.Fatalf("err %v", err)
	}
	want := fmt.Sprintf("rename old web2,update old memory=%d swap=%d nano=500000000,rename old web", 256<<20, 512<<20)
	if got := strings.Join(*calls, ","); got != want {
		t.Fatalf("calls\n %s\nwant\n %s", got, want)
	}
}

func TestRecreatePassesDockersWarningsOn(t *testing.T) {
	f, _ := editFake()
	f.ContainerCreateFn = func(context.Context, client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
		return client.ContainerCreateResult{ID: "new", Warnings: []string{"memory limit not supported"}}, nil
	}
	res, err := New(f).Edit(context.Background(), "web", changed())
	if err != nil || !res.Recreated || !slices.Equal(res.Warnings, []string{"memory limit not supported"}) {
		t.Fatalf("%+v %v", res, err)
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
	// Docker's warnings of the creation reach the result of a recreation that succeeds, also when the old container
	// cannot be removed (then next to Warning)
	createWarnings := []string{"memory limit not supported"}
	for _, tc := range cases {
		f, calls := editFake(tc.fail...)
		create := f.ContainerCreateFn
		f.ContainerCreateFn = func(ctx context.Context, o client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
			res, err := create(ctx, o)
			if err == nil {
				res.Warnings = createWarnings
			}
			return res, err
		}
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
		case tc.code == "" && !slices.Equal(res.Warnings, createWarnings):
			t.Errorf("fail %v: warnings %v, want Docker's %v", tc.fail, res.Warnings, createWarnings)
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

// A CPU limit above the CPUs of the host is refused (400, field resources.cpus) before anything changes, the rename that
// comes with it included; one within them goes through, and docker info is asked for once per edit.
func TestEditRouteChecksACPULimitAgainstTheHost(t *testing.T) {
	f, calls := editFake()
	infos := 0
	f.InfoFn = func(context.Context, client.InfoOptions) (client.SystemInfoResult, error) {
		infos++
		return client.SystemInfoResult{Info: system.Info{NCPU: 2}}, nil
	}
	app := testApp(f)
	c, img := inspected()
	for _, tc := range []struct {
		cpus   float64
		status int
	}{{0.01, 200}, {2, 200}, {2.01, 400}, {3, 400}} {
		infos, *calls = 0, nil
		s := SpecOf(c, img)
		s.Name, s.Resources.CPUs = "web2", tc.cpus
		resp := sdktestDo(t, app, "PUT", "/containers/web", toJSONUnit(t, s.Normalize()))
		raw := readAll(t, resp)
		if resp.StatusCode != tc.status || infos != 1 {
			t.Errorf("cpus %v: %d (want %d), docker info asked for %d times (want 1): %s", tc.cpus, resp.StatusCode, tc.status, infos, raw)
		}
		if tc.status == 400 {
			if !strings.Contains(raw, `"invalid_container_spec"`) || !strings.Contains(raw, `"field":"resources.cpus"`) {
				t.Errorf("cpus %v: %s", tc.cpus, raw)
			}
			if len(*calls) != 0 {
				t.Errorf("cpus %v: calls %v, want none: a refused form changes nothing", tc.cpus, *calls)
			}
		} else if len(*calls) != 2 || (*calls)[0] != "rename old web2" || !strings.HasPrefix((*calls)[1], "update old memory=") {
			t.Errorf("cpus %v: calls %v, want the rename and the update", tc.cpus, *calls)
		}
	}
}

// Only a CPU limit needs the number of CPUs of the host: without one, docker info is not asked for, whether the edit is
// direct, recreates (a removed limit) or changes nothing.
func TestEditRouteWithoutACPULimitDoesNotAskForTheHostInfo(t *testing.T) {
	cases := []struct {
		name      string
		haveNano  int64 // the CPU limit of the container (inspected() has none)
		change    func(*Spec)
		recreated bool
	}{
		{"a memory limit changed", 0, func(s *Spec) { s.Resources.Memory = 512 << 20 }, false},
		{"a rename", 0, func(s *Spec) { s.Name = "web2" }, false},
		{"nothing changed", 0, func(*Spec) {}, false},
		{"the memory limit removed", 0, func(s *Spec) { s.Resources.Memory = 0 }, true},
		{"the CPU limit removed", 1_000_000_000, func(s *Spec) { s.Resources.CPUs = 0 }, true},
	}
	for _, tc := range cases {
		f, _ := editFake()
		c, img := inspected()
		c.HostConfig.NanoCPUs = tc.haveNano
		f.ContainerInspectFn = func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: c}, nil
		}
		f.InfoFn = func(context.Context, client.InfoOptions) (client.SystemInfoResult, error) {
			t.Errorf("%s: docker info was asked for, though the form has no CPU limit", tc.name)
			return client.SystemInfoResult{}, nil
		}
		s := SpecOf(c, img)
		tc.change(&s)
		if s.Resources.CPUs != 0 {
			t.Fatalf("%s: the form must have no CPU limit: %+v", tc.name, s.Resources)
		}
		resp := sdktestDo(t, testApp(f), "PUT", "/containers/web", toJSONUnit(t, s.Normalize()))
		raw := readAll(t, resp)
		if resp.StatusCode != 200 || strings.Contains(raw, `"recreated":true`) != tc.recreated {
			t.Errorf("%s: %d %s, want recreated=%v", tc.name, resp.StatusCode, raw, tc.recreated)
		}
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
