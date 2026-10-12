package containers

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"strings"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
)

// Sample is one point of a container's statistics; rates are bytes per second over the last interval.
type Sample struct {
	Time       time.Time `json:"time"`
	CPUPercent float64   `json:"cpuPercent"`
	// MemUsage and MemLimit are null without memory accounting (no memory cgroup, typical on a Raspberry Pi without
	// cgroup_enable=memory).
	MemUsage *uint64 `json:"memUsage"`
	MemLimit *uint64 `json:"memLimit"`
	NetRx    float64 `json:"netRx"`
	NetTx    float64 `json:"netTx"`
	BlkRead  float64 `json:"blkRead"`
	BlkWrite float64 `json:"blkWrite"`
	PIDs     uint64  `json:"pids"`
}

func (s *Service) stats(c fiber.Ctx, stream *sse.Stream) error {
	target := c.Locals(statsKey).(streamTarget) // the only use of c, at the start (see sdk.SSE)
	if !target.running {
		_ = stream.Event(sse.Event{Name: "stopped", Data: "{}"})
		return nil
	}
	ctx, cancel := streamContext(stream)
	defer cancel()
	res, err := s.Docker.ContainerStats(ctx, target.id, client.ContainerStatsOptions{Stream: true})
	if err != nil {
		_ = stream.Event(sse.Event{Name: "error", Data: map[string]string{"message": dockerapi.Message(err)}})
		return nil
	}
	defer res.Body.Close()
	dec := json.NewDecoder(res.Body)
	var sm sampler
	for {
		var rec container.StatsResponse
		if err := dec.Decode(&rec); err != nil {
			switch {
			case ctx.Err() != nil || stream.Err() != nil: // the browser left (a failed read is its doing)
			case errors.Is(err, io.EOF): // the daemon ended the stream: the container stopped
				_ = stream.Event(sse.Event{Name: "stopped", Data: "{}"})
			default: // a broken connection or a record that is no JSON: not a stop
				_ = stream.Event(sse.Event{Name: "error", Data: map[string]string{"message": dockerapi.Message(err)}})
			}
			return nil
		}
		// The daemon does not end the stream when the container stops: it goes on with an empty record per second (only
		// ID and name, no read time) until the client leaves.
		if rec.Read.IsZero() {
			_ = stream.Event(sse.Event{Name: "stopped", Data: "{}"})
			return nil
		}
		sample, ok := sm.add(rec, s.StatsInterval)
		if !ok {
			continue
		}
		if err := stream.Event(sse.Event{Name: "stats", Data: sample}); err != nil {
			return nil
		}
	}
}

// sampler turns Docker's records (about one per second) into a sample per interval, with rates over that interval.
type sampler struct {
	last *container.StatsResponse // the record of the last sample (or the first record)
}

func (sm *sampler) add(rec container.StatsResponse, interval time.Duration) (Sample, bool) {
	if sm.last == nil {
		sm.last = &rec
		return Sample{}, false
	}
	elapsed := rec.Read.Sub(sm.last.Read)
	if elapsed <= 0 || elapsed < interval*9/10 {
		return Sample{}, false
	}
	prev := *sm.last
	sm.last = &rec
	return sampleOf(prev, rec, elapsed.Seconds()), true
}

func sampleOf(prev, cur container.StatsResponse, seconds float64) Sample {
	s := Sample{Time: cur.Read, CPUPercent: math.Round(cpuPercent(prev.CPUStats, cur.CPUStats)*10) / 10, PIDs: cur.PidsStats.Current}
	if used, limit, ok := memory(cur.MemoryStats); ok {
		s.MemUsage, s.MemLimit = &used, &limit
	}
	rx0, tx0 := netBytes(prev)
	rx1, tx1 := netBytes(cur)
	r0, w0 := blkBytes(prev)
	r1, w1 := blkBytes(cur)
	s.NetRx, s.NetTx = rate(rx0, rx1, seconds), rate(tx0, tx1, seconds)
	s.BlkRead, s.BlkWrite = rate(r0, r1, seconds), rate(w0, w1, seconds)
	return s
}

// cpuPercent between two records, as docker stats computes it: the container's share of the host's CPU time, times the
// number of CPUs.
func cpuPercent(prev, cur container.CPUStats) float64 {
	cpuDelta := float64(cur.CPUUsage.TotalUsage) - float64(prev.CPUUsage.TotalUsage)
	sysDelta := float64(cur.SystemUsage) - float64(prev.SystemUsage)
	n := float64(cur.OnlineCPUs)
	if n == 0 {
		n = float64(len(cur.CPUUsage.PercpuUsage))
	}
	if n == 0 {
		n = 1
	}
	if sysDelta <= 0 || cpuDelta < 0 {
		return 0
	}
	return cpuDelta / sysDelta * n * 100
}

// memory is the usage without the inactive file cache (as docker stats shows it) and the limit; ok is false without
// memory accounting.
func memory(m container.MemoryStats) (used, limit uint64, ok bool) {
	if m.Usage == 0 && m.Limit == 0 {
		return 0, 0, false
	}
	used = m.Usage
	for _, key := range []string{"inactive_file", "total_inactive_file"} { // cgroup v2, cgroup v1
		if v, found := m.Stats[key]; found && v < used {
			used -= v
			break
		}
	}
	return used, m.Limit, true
}

func netBytes(r container.StatsResponse) (rx, tx uint64) {
	for _, n := range r.Networks {
		rx += n.RxBytes
		tx += n.TxBytes
	}
	return rx, tx
}

func blkBytes(r container.StatsResponse) (read, write uint64) {
	for _, e := range r.BlkioStats.IoServiceBytesRecursive {
		switch {
		case strings.EqualFold(e.Op, "read"):
			read += e.Value
		case strings.EqualFold(e.Op, "write"):
			write += e.Value
		}
	}
	return read, write
}

func rate(prev, cur uint64, seconds float64) float64 {
	if cur < prev || seconds <= 0 { // a counter reset
		return 0
	}
	return math.Round(float64(cur-prev) / seconds)
}
