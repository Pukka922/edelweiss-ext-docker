package containers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

var t0 = time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)

// record is a stats record at t0+sec with cumulative counters.
func record(sec int, cpu, system uint64, mem uint64, rx, tx, read, write uint64) container.StatsResponse {
	r := container.StatsResponse{Read: t0.Add(time.Duration(sec) * time.Second)}
	r.CPUStats.CPUUsage.TotalUsage, r.CPUStats.SystemUsage, r.CPUStats.OnlineCPUs = cpu, system, 4
	if mem > 0 {
		r.MemoryStats = container.MemoryStats{Usage: mem, Limit: 1 << 30, Stats: map[string]uint64{"inactive_file": 1 << 20}}
	}
	r.Networks = map[string]container.NetworkStats{"eth0": {RxBytes: rx, TxBytes: tx}}
	r.BlkioStats.IoServiceBytesRecursive = []container.BlkioStatEntry{{Op: "read", Value: read}, {Op: "Write", Value: write}}
	r.PidsStats.Current = 7
	return r
}

func TestSampler(t *testing.T) {
	var sm sampler
	if _, ok := sm.add(record(0, 0, 0, 10<<20, 0, 0, 0, 0), 2*time.Second); ok {
		t.Fatal("the first record gives no sample")
	}
	if _, ok := sm.add(record(1, 1e9, 4e9, 10<<20, 1000, 0, 0, 0), 2*time.Second); ok {
		t.Fatal("one second is less than the interval")
	}
	s, ok := sm.add(record(2, 2e9, 8e9, 10<<20, 4000, 2000, 8192, 4096), 2*time.Second)
	if !ok {
		t.Fatal("two seconds give a sample")
	}
	// CPU: 2e9 of 8e9 system time on 4 CPUs = 100 %; rates over the 2 seconds since the first record
	if s.CPUPercent != 100 || s.NetRx != 2000 || s.NetTx != 1000 || s.BlkRead != 4096 || s.BlkWrite != 2048 || s.PIDs != 7 {
		t.Fatalf("sample %+v", s)
	}
	if s.MemUsage == nil || *s.MemUsage != 9<<20 || *s.MemLimit != 1<<30 {
		t.Fatalf("memory %v %v (usage without the inactive file cache)", s.MemUsage, s.MemLimit)
	}
	s, _ = sm.add(record(4, 2e9, 16e9, 0, 4000, 2000, 8192, 4096), 2*time.Second)
	if s.MemUsage != nil || s.MemLimit != nil || s.CPUPercent != 0 || s.NetRx != 0 {
		t.Fatalf("without memory accounting and without activity: %+v", s)
	}
}

func statsFake(running bool, records ...container.StatsResponse) (*dockertest.Fake, *bool) {
	var body bytes.Buffer
	for _, r := range records {
		_ = json.NewEncoder(&body).Encode(r)
	}
	return statsFakeBody(running, &body)
}

func statsFakeBody(running bool, body io.Reader) (*dockertest.Fake, *bool) {
	called := false
	return &dockertest.Fake{
		ContainerInspectFn: func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: container.InspectResponse{ID: id, State: &container.State{Running: running}}}, nil
		},
		ContainerStatsFn: func(_ context.Context, _ string, o client.ContainerStatsOptions) (client.ContainerStatsResult, error) {
			called = true
			if !o.Stream {
				return client.ContainerStatsResult{}, io.ErrUnexpectedEOF
			}
			return client.ContainerStatsResult{Body: io.NopCloser(body)}, nil
		},
	}, &called
}

func TestStats(t *testing.T) {
	f, _ := statsFake(true, record(0, 0, 0, 10<<20, 0, 0, 0, 0), record(1, 1e9, 4e9, 10<<20, 0, 0, 0, 0), record(2, 2e9, 8e9, 10<<20, 0, 0, 0, 0))
	resp := sdktest.Do(t, testApp(f), sdktest.Request("GET", "/containers/web/stats", nil, anna))
	names, data := sseEvents(t, resp.Body)
	if strings.Join(names, ",") != "stats,stopped" || !strings.Contains(data[0], `"cpuPercent":100`) {
		t.Fatalf("events %v %v", names, data)
	}
	stopped, called := statsFake(false)
	resp = sdktest.Do(t, testApp(stopped), sdktest.Request("GET", "/containers/db/stats", nil, anna))
	if names, _ := sseEvents(t, resp.Body); strings.Join(names, ",") != "stopped" || *called {
		t.Fatalf("stopped container: %v, stats called %v", names, *called)
	}
}

// The daemon does not end the stream of a container that stops: it goes on with empty records (no read time, no
// counters). Such a record ends the stream with stopped, and what follows it is not read.
func TestStatsEndAtTheEmptyRecordOfAStoppedContainer(t *testing.T) {
	f, _ := statsFake(true, record(0, 0, 0, 10<<20, 0, 0, 0, 0), record(2, 2e9, 8e9, 10<<20, 0, 0, 0, 0),
		container.StatsResponse{ID: "web"}, record(4, 4e9, 16e9, 10<<20, 0, 0, 0, 0))
	resp := sdktest.Do(t, testApp(f), sdktest.Request("GET", "/containers/web/stats", nil, anna))
	if names, data := sseEvents(t, resp.Body); strings.Join(names, ",") != "stats,stopped" || !strings.Contains(data[0], `"cpuPercent":100`) {
		t.Fatalf("events %v %v", names, data)
	}
}

// Only the end of the stream (EOF) means that the container stopped. A broken connection or a record that is no JSON is an
// error, and nothing is sampled from what follows.
func TestStatsFailuresAreErrors(t *testing.T) {
	var good bytes.Buffer
	for _, r := range []container.StatsResponse{record(0, 0, 0, 10<<20, 0, 0, 0, 0), record(2, 2e9, 8e9, 10<<20, 0, 0, 0, 0)} {
		_ = json.NewEncoder(&good).Encode(r)
	}
	for _, tc := range []struct {
		name string
		then io.Reader
		want string
	}{
		{"a record that is no JSON", strings.NewReader("this is not json\n"), "invalid character"},
		{"a record that is cut off", strings.NewReader(`{"read":"2026-10-10T12:00:`), "unexpected EOF"},
		{"a broken connection", iotest.ErrReader(errors.New("connection reset")), "connection reset"},
	} {
		f, _ := statsFakeBody(true, io.MultiReader(bytes.NewReader(good.Bytes()), tc.then))
		resp := sdktest.Do(t, testApp(f), sdktest.Request("GET", "/containers/web/stats", nil, anna))
		names, data := sseEvents(t, resp.Body)
		if strings.Join(names, ",") != "stats,error" || !strings.Contains(data[1], `"message"`) || !strings.Contains(data[1], tc.want) {
			t.Errorf("%s: events %v %v", tc.name, names, data)
		}
	}
}

// A restarting container is "running" for Docker, but has no statistics: the daemon would answer a subscription with
// empty records. It is stopped for the stream, without a subscription.
func TestStatsOfARestartingContainerAreStopped(t *testing.T) {
	f, called := statsFake(true, record(0, 0, 0, 10<<20, 0, 0, 0, 0))
	f.ContainerInspectFn = func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{Container: container.InspectResponse{ID: id, State: &container.State{Running: true, Restarting: true}}}, nil
	}
	resp := sdktest.Do(t, testApp(f), sdktest.Request("GET", "/containers/web/stats", nil, anna))
	if names, _ := sseEvents(t, resp.Body); strings.Join(names, ",") != "stopped" || *called {
		t.Fatalf("restarting container: %v, stats called %v", names, *called)
	}
}

func TestRate(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prev, cur uint64
		seconds   float64
		want      float64
	}{
		{"growth", 10, 20, 2, 5},
		{"no growth", 5, 5, 2, 0},
		{"rounded to bytes", 7, 10, 2, 2},
		{"counter reset", 20, 10, 2, 0}, // the container restarted, or the counter wrapped: no negative rate
		{"no time", 0, 10, 0, 0},
		{"negative time", 0, 10, -1, 0},
		{"large counters", 1 << 40, 1<<40 + 1<<20, 1, 1 << 20},
	} {
		if got := rate(tc.prev, tc.cur, tc.seconds); got != tc.want {
			t.Errorf("%s: rate(%d, %d, %v) = %v, want %v", tc.name, tc.prev, tc.cur, tc.seconds, got, tc.want)
		}
	}
}

func TestCPUPercent(t *testing.T) {
	cpu := func(total, system uint64, online uint32, percpu ...uint64) container.CPUStats {
		var c container.CPUStats
		c.CPUUsage.TotalUsage, c.SystemUsage, c.OnlineCPUs, c.CPUUsage.PercpuUsage = total, system, online, percpu
		return c
	}
	for _, tc := range []struct {
		name      string
		prev, cur container.CPUStats
		want      float64
	}{
		{"share of the host times the CPUs", cpu(0, 0, 4), cpu(2e9, 8e9, 4), 100},
		{"one of four CPUs busy", cpu(0, 0, 4), cpu(1e9, 4e9, 4), 100},
		{"quarter of a CPU", cpu(0, 0, 4), cpu(1e9, 16e9, 4), 25},
		{"idle", cpu(5, 100, 4), cpu(5, 200, 4), 0},
		{"no system time passed", cpu(0, 100, 4), cpu(1e9, 100, 4), 0},
		{"system time ran backwards", cpu(0, 200, 4), cpu(1e9, 100, 4), 0},
		{"container time ran backwards (restart)", cpu(9e9, 0, 4), cpu(1e9, 4e9, 4), 0},
		{"no online CPUs: the per-CPU list counts", cpu(0, 0, 0, 1, 1), cpu(1e9, 4e9, 0, 1, 1), 50},
		{"neither: one CPU", cpu(0, 0, 0), cpu(1e9, 4e9, 0), 25},
		{"nothing at all", cpu(0, 0, 0), cpu(0, 0, 0), 0},
	} {
		if got := cpuPercent(tc.prev, tc.cur); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestMemory(t *testing.T) {
	stats := func(usage uint64, cache map[string]uint64) container.MemoryStats {
		return container.MemoryStats{Usage: usage, Limit: 1000, Stats: cache}
	}
	for _, tc := range []struct {
		name        string
		in          container.MemoryStats
		used, limit uint64
		ok          bool
	}{
		{"no accounting", container.MemoryStats{}, 0, 0, false},
		{"without a cache entry", stats(100, nil), 100, 1000, true},
		{"cgroup v2: inactive_file", stats(100, map[string]uint64{"inactive_file": 30}), 70, 1000, true},
		{"cgroup v1: total_inactive_file", stats(100, map[string]uint64{"total_inactive_file": 40}), 60, 1000, true},
		{"both: the v2 key counts", stats(100, map[string]uint64{"inactive_file": 30, "total_inactive_file": 40}), 70, 1000, true},
		{"cache as large as the usage is not subtracted", stats(100, map[string]uint64{"inactive_file": 100}), 100, 1000, true},
		{"cache larger than the usage is not subtracted", stats(100, map[string]uint64{"inactive_file": 150}), 100, 1000, true},
		{"other entries are ignored", stats(100, map[string]uint64{"active_file": 30}), 100, 1000, true},
		{"a limit without usage", stats(0, nil), 0, 1000, true},
	} {
		used, limit, ok := memory(tc.in)
		if used != tc.used || limit != tc.limit || ok != tc.ok {
			t.Errorf("%s: %d %d %v, want %d %d %v", tc.name, used, limit, ok, tc.used, tc.limit, tc.ok)
		}
	}
}

func TestBlockAndNetworkCounters(t *testing.T) {
	r := container.StatsResponse{Networks: map[string]container.NetworkStats{"eth0": {RxBytes: 10, TxBytes: 1}, "eth1": {RxBytes: 5, TxBytes: 2}}}
	r.BlkioStats.IoServiceBytesRecursive = []container.BlkioStatEntry{
		{Op: "Read", Value: 100}, {Op: "read", Value: 50}, {Op: "Write", Value: 7}, {Op: "Total", Value: 157}, {Op: "Sync", Value: 1},
	}
	if rx, tx := netBytes(r); rx != 15 || tx != 3 {
		t.Errorf("network %d %d (all interfaces)", rx, tx)
	}
	if read, write := blkBytes(r); read != 150 || write != 7 {
		t.Errorf("block %d %d (Read and Write of all devices, other operations ignored)", read, write)
	}
}

// recordAt is a record t0+ms milliseconds, without counters.
func recordAt(ms int) container.StatsResponse {
	return container.StatsResponse{Read: t0.Add(time.Duration(ms) * time.Millisecond)}
}

// Records that are out of order or have the same time give no sample and do not replace the record that the next rates
// are computed from.
func TestSamplerIgnoresRecordsThatAreNotNewer(t *testing.T) {
	var sm sampler
	_, _ = sm.add(record(10, 0, 0, 10<<20, 0, 0, 0, 0), 2*time.Second)
	for name, rec := range map[string]container.StatsResponse{
		"same time":  record(10, 5e9, 8e9, 10<<20, 999, 0, 0, 0),
		"older":      record(7, 5e9, 8e9, 10<<20, 999, 0, 0, 0),
		"much older": record(-100, 5e9, 8e9, 10<<20, 999, 0, 0, 0),
	} {
		if _, ok := sm.add(rec, 2*time.Second); ok {
			t.Errorf("%s: no sample expected", name)
		}
	}
	s, ok := sm.add(record(12, 2e9, 8e9, 10<<20, 4000, 0, 0, 0), 2*time.Second)
	if !ok || s.CPUPercent != 100 || s.NetRx != 2000 {
		t.Fatalf("the rates still count from the first record: %+v %v", s, ok)
	}
	// the next sample comes 1.8 s (9/10 of the interval) after the last one at the earliest
	for _, tc := range []struct {
		ms int
		ok bool
	}{{13000, false}, {13700, false}, {13900, true}} {
		if _, ok := sm.add(recordAt(tc.ms), 2*time.Second); ok != tc.ok {
			t.Errorf("record at %d ms after 12 s: sample %v, want %v", tc.ms, ok, tc.ok)
		}
	}
}
