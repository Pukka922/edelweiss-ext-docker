package images

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/moby/moby/api/types/jsonstream"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/registries"
	"github.com/Pukka922/edelweiss/sdk"
)

// Layer is the progress of one layer of a pull.
type Layer struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Current int64  `json:"current"`
	Total   int64  `json:"total"`
}

// Progress is a snapshot of a pull.
type Progress struct {
	Ref string `json:"ref"`
	// Status is the latest line that is not about a layer, e.g. "Pulling from library/busybox".
	Status string  `json:"status"`
	Layers []Layer `json:"layers"`
	Done   bool    `json:"done"`
	Error  string  `json:"error,omitempty"`
}

// Pulls runs image pulls in the background: they outlive the request and the browser, keep the extension's process
// busy while they run, and stay readable for Keep after they end.
type Pulls struct {
	Keep time.Duration
	Now  func() time.Time

	docker dockerapi.Docker
	store  *registries.Store
	mu     sync.Mutex
	jobs   map[string]*Pull
}

// NewPulls returns the pull manager; logins come from s.
func NewPulls(d dockerapi.Docker, s *registries.Store) *Pulls {
	return &Pulls{Keep: 10 * time.Minute, Now: time.Now, docker: d, store: s, jobs: map[string]*Pull{}}
}

// Pull is one running or finished pull.
type Pull struct {
	id      string
	mu      sync.Mutex
	state   Progress
	index   map[string]int // layer ID → position in state.Layers
	changed chan struct{}  // closed (and replaced) on every change
	ended   time.Time
}

// ID identifies the pull in GET /pulls/:id/events.
func (p *Pull) ID() string { return p.id }

// Watch returns the current progress and a channel that is closed at the next change.
func (p *Pull) Watch() (Progress, <-chan struct{}) {
	p.mu.Lock()
	defer p.mu.Unlock()
	snap := p.state
	snap.Layers = slices.Clone(p.state.Layers) // an empty list stays non-nil, so it encodes as [] and not as null
	return snap, p.changed
}

func (p *Pull) notify() {
	close(p.changed)
	p.changed = make(chan struct{})
}

func (p *Pull) update(m jsonstream.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if m.ID == "" || strings.HasPrefix(m.Status, "Pulling from") {
		p.state.Status = m.Status
		p.notify()
		return
	}
	i, ok := p.index[m.ID]
	if !ok {
		i = len(p.state.Layers)
		p.index[m.ID] = i
		p.state.Layers = append(p.state.Layers, Layer{ID: m.ID})
	}
	l := &p.state.Layers[i]
	l.Status = m.Status
	switch {
	case m.Progress != nil && m.Progress.Total > 0:
		l.Current, l.Total = m.Progress.Current, m.Progress.Total
	case strings.HasSuffix(m.Status, "complete") || m.Status == "Already exists":
		l.Current = l.Total
	}
	p.notify()
}

func (p *Pull) finish(now time.Time, errMsg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state.Done, p.state.Error, p.ended = true, errMsg, now
	p.notify()
}

// Start begins pulling ref with the stored login of its registry and returns at once.
func (ps *Pulls) Start(ref string) (*Pull, error) {
	auth, err := ps.store.AuthFor(ref)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	p := &Pull{id: hex.EncodeToString(b), state: Progress{Ref: ref, Layers: []Layer{}}, index: map[string]int{}, changed: make(chan struct{})}
	ps.mu.Lock()
	ps.sweep()
	ps.jobs[p.id] = p
	ps.mu.Unlock()
	done := sdk.Busy()
	go func() {
		defer done()
		errMsg := ps.run(p, ref, auth)
		p.finish(ps.Now(), errMsg) // stamped when the pull ends: Keep counts from the end, not from the start
	}()
	return p, nil
}

// run pulls and returns the error text, "" on success.
func (ps *Pulls) run(p *Pull, ref, auth string) string {
	ctx := context.Background()
	resp, err := ps.docker.ImagePull(ctx, ref, client.ImagePullOptions{RegistryAuth: auth})
	if err != nil {
		return dockerapi.Message(err)
	}
	for m, err := range resp.JSONMessages(ctx) {
		if err != nil {
			return err.Error()
		}
		if m.Error != nil {
			return m.Error.Message
		}
		p.update(m)
	}
	return ""
}

// Get returns the pull id while it runs and for Keep after it ended.
func (ps *Pulls) Get(id string) (*Pull, bool) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	ps.sweep()
	p, ok := ps.jobs[id]
	return p, ok
}

// sweep drops pulls that ended more than Keep ago; ps.mu is held.
func (ps *Pulls) sweep() {
	limit := ps.Now().Add(-ps.Keep)
	for id, p := range ps.jobs {
		p.mu.Lock()
		expired := p.state.Done && p.ended.Before(limit)
		p.mu.Unlock()
		if expired {
			delete(ps.jobs, id)
		}
	}
}

var (
	imagePattern = regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*(/[a-z0-9]+([._-][a-z0-9]+)*)*$`)
	tagPattern   = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,127}$`)
)

// Reference composes host/image:tag; host "" and Docker Hub need no prefix, tag "" is latest.
func Reference(host, image, tag string) (string, error) {
	image, tag = strings.TrimSpace(image), strings.TrimSpace(tag)
	if !imagePattern.MatchString(image) {
		return "", errors.New("image must be a name like library/nginx (lower case)")
	}
	if tag == "" {
		tag = "latest"
	}
	if !tagPattern.MatchString(tag) {
		return "", errors.New("tag may only contain letters, digits and _ . - and must not start with . or -")
	}
	if host == "" || host == registries.DockerHub {
		return image + ":" + tag, nil
	}
	return host + "/" + image + ":" + tag, nil
}
