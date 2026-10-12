package images

import (
	"context"
	"errors"
	"io"
	"iter"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/pkg/authconfig"
	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss-ext-docker/internal/registries"
)

// fakePull is a client.ImagePullResponse fed by the test: every message sent on msgs is yielded, closing msgs ends the
// stream.
type fakePull struct {
	io.ReadCloser
	msgs chan jsonstream.Message
}

func newFakePull() *fakePull {
	return &fakePull{ReadCloser: io.NopCloser(strings.NewReader("")), msgs: make(chan jsonstream.Message)}
}

func (f *fakePull) JSONMessages(ctx context.Context) iter.Seq2[jsonstream.Message, error] {
	return func(yield func(jsonstream.Message, error) bool) {
		for {
			select {
			case m, ok := <-f.msgs:
				if !ok || !yield(m, nil) {
					return
				}
			case <-ctx.Done():
				yield(jsonstream.Message{}, ctx.Err())
				return
			}
		}
	}
}

func (f *fakePull) Wait(ctx context.Context) error { return nil }

func fmtErr(s string) error { return errors.New(s) }

func testStore(t *testing.T) *registries.Store {
	s := registries.NewStore(filepath.Join(t.TempDir(), "registries.db"))
	s.Chown = func(string, int, int) error { return nil }
	return s
}

// waitFor polls the pull until cond holds or a second has passed.
func waitFor(t *testing.T, p *Pull, cond func(Progress) bool) Progress {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		snap, changed := p.Watch()
		if cond(snap) {
			return snap
		}
		select {
		case <-changed:
		case <-deadline:
			t.Fatalf("condition not reached; last progress %+v", snap)
		}
	}
}

func TestPullProgress(t *testing.T) {
	fp := newFakePull()
	var gotRef, gotAuth string
	f := &dockertest.Fake{ImagePullFn: func(_ context.Context, ref string, o client.ImagePullOptions) (client.ImagePullResponse, error) {
		gotRef, gotAuth = ref, o.RegistryAuth
		return fp, nil
	}}
	s := testStore(t)
	if _, err := s.Add(registries.Input{Name: "Hub", Address: "docker.io", Auth: true, Username: "pukka", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	pulls := NewPulls(f, s)
	p, err := pulls.Start("nginx:1.29")
	if err != nil {
		t.Fatal(err)
	}
	if first, _ := p.Watch(); first.Layers == nil {
		t.Fatal("a pull without layers yet must report an empty list, not nil (JSON null)")
	}
	fp.msgs <- jsonstream.Message{ID: "1.29", Status: "Pulling from library/nginx"}
	fp.msgs <- jsonstream.Message{ID: "aaaa", Status: "Downloading", Progress: &jsonstream.Progress{Current: 10, Total: 100}}
	fp.msgs <- jsonstream.Message{ID: "bbbb", Status: "Waiting"}
	fp.msgs <- jsonstream.Message{ID: "aaaa", Status: "Download complete"}
	snap := waitFor(t, p, func(p Progress) bool { return len(p.Layers) == 2 && p.Layers[0].Status == "Download complete" })
	if snap.Status != "Pulling from library/nginx" || snap.Layers[0].ID != "aaaa" || snap.Layers[0].Current != 100 || snap.Layers[1].ID != "bbbb" || snap.Done {
		t.Fatalf("progress %+v", snap)
	}
	close(fp.msgs)
	snap = waitFor(t, p, func(p Progress) bool { return p.Done })
	if snap.Error != "" {
		t.Fatalf("error %q", snap.Error)
	}
	if gotRef != "nginx:1.29" {
		t.Fatalf("pulled %q", gotRef)
	}
	cfg, err := authconfig.Decode(gotAuth)
	if err != nil || cfg.Username != "pukka" || cfg.ServerAddress != "https://index.docker.io/v1/" {
		t.Fatalf("auth %+v %v", cfg, err)
	}
}

func TestPullFailures(t *testing.T) {
	fp := newFakePull()
	calls := 0
	f := &dockertest.Fake{ImagePullFn: func(_ context.Context, ref string, _ client.ImagePullOptions) (client.ImagePullResponse, error) {
		calls++
		if ref == "missing:1" {
			return nil, fmtErr("Error response from daemon: manifest for missing:1 not found")
		}
		return fp, nil
	}}
	pulls := NewPulls(f, testStore(t))
	p, _ := pulls.Start("missing:1")
	if snap := waitFor(t, p, func(p Progress) bool { return p.Done }); snap.Error != "manifest for missing:1 not found" {
		t.Fatalf("error before the stream: %+v", snap)
	}
	p, _ = pulls.Start("busybox:1.37")
	fp.msgs <- jsonstream.Message{Error: &jsonstream.Error{Message: "pull access denied"}}
	if snap := waitFor(t, p, func(p Progress) bool { return p.Done }); snap.Error != "pull access denied" {
		t.Fatalf("error in the stream: %+v", snap)
	}
}

func TestFinishedPullsExpire(t *testing.T) {
	fp := newFakePull()
	f := &dockertest.Fake{ImagePullFn: func(context.Context, string, client.ImagePullOptions) (client.ImagePullResponse, error) {
		return fp, nil
	}}
	pulls := NewPulls(f, testStore(t))
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	pulls.Now = func() time.Time { return now }
	p, _ := pulls.Start("busybox:1.37")
	close(fp.msgs)
	waitFor(t, p, func(p Progress) bool { return p.Done })
	now = now.Add(9 * time.Minute)
	if _, ok := pulls.Get(p.ID()); !ok {
		t.Fatal("a pull finished 9 minutes ago must still be there")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := pulls.Get(p.ID()); ok {
		t.Fatal("a pull finished 11 minutes ago must be gone")
	}
}

// fakeClock is a clock for Pulls.Now that tests move; the pull goroutine reads it, so it is safe for concurrent use.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func TestRetentionCountsFromTheEnd(t *testing.T) {
	fp := newFakePull()
	started := make(chan struct{})
	f := &dockertest.Fake{ImagePullFn: func(context.Context, string, client.ImagePullOptions) (client.ImagePullResponse, error) {
		close(started)
		return fp, nil
	}}
	pulls := NewPulls(f, testStore(t))
	clock := &fakeClock{now: time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)}
	pulls.Now = clock.Now
	p, _ := pulls.Start("busybox:1.37")
	<-started
	clock.Advance(30 * time.Minute) // the pull runs for longer than Keep
	if _, ok := pulls.Get(p.ID()); !ok {
		t.Fatal("a running pull must never expire")
	}
	close(fp.msgs)
	waitFor(t, p, func(p Progress) bool { return p.Done })
	clock.Advance(9 * time.Minute)
	if _, ok := pulls.Get(p.ID()); !ok {
		t.Fatal("a pull that ended 9 minutes ago must still be there, however long it ran")
	}
	clock.Advance(2 * time.Minute)
	if _, ok := pulls.Get(p.ID()); ok {
		t.Fatal("a pull that ended 11 minutes ago must be gone")
	}
}

func TestReference(t *testing.T) {
	good := []struct{ host, image, tag, want string }{
		{"", "nginx", "", "nginx:latest"},
		{registries.DockerHub, "library/nginx", "1.29", "library/nginx:1.29"},
		{"ghcr.io", "octo/app", "v1.2", "ghcr.io/octo/app:v1.2"},
		{"localhost:5000", "test/busybox", "1.37", "localhost:5000/test/busybox:1.37"},
	}
	for _, g := range good {
		if got, err := Reference(g.host, g.image, g.tag); err != nil || got != g.want {
			t.Errorf("Reference(%q,%q,%q) = %q, %v; want %q", g.host, g.image, g.tag, got, err, g.want)
		}
	}
	for _, bad := range [][3]string{{"", "", "x"}, {"", "Nginx", ""}, {"", "a b", ""}, {"", "nginx", "bad tag"}, {"", "nginx", ".x"}} {
		if got, err := Reference(bad[0], bad[1], bad[2]); err == nil {
			t.Errorf("Reference(%q) = %q, want an error", bad, got)
		}
	}
}
