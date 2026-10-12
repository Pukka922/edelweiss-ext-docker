package containers

import (
	"errors"
	"math"
	"strings"
	"testing"
)

func TestNanoCPUs(t *testing.T) {
	// 0.1 + 0.2 as variables: a constant expression would be exact, but the sum of the floats is 0.30000000000000004
	tenth, fifth := 0.1, 0.2
	for _, c := range []struct {
		cpus float64
		want int64
	}{{0, 0}, {0.01, 10_000_000}, {0.3, 300_000_000}, {1.5, 1_500_000_000}, {tenth + fifth, 300_000_000}} {
		if got := nanoCPUs(c.cpus); got != c.want {
			t.Errorf("nanoCPUs(%v) = %d, want %d", c.cpus, got, c.want)
		}
		if got := nanoCPUs(cpusOf(c.want)); got != c.want {
			t.Errorf("round trip of %d gives %d", c.want, got)
		}
	}
}

func TestMemorySwap(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name                         string
		baseMemory, baseSwap, memory int64
		want                         int64
		fails                        bool
	}{
		{"no limit", 512 * mib, 1024 * mib, 0, 0, false},
		{"none before", 0, 0, 512 * mib, 1024 * mib, false},
		{"docker's default before", 512 * mib, 1024 * mib, 2048 * mib, 4096 * mib, false},
		{"unlimited swap", 512 * mib, -1, 2048 * mib, -1, false},
		{"custom swap that fits", 512 * mib, 3000 * mib, 2048 * mib, 3000 * mib, false},
		{"custom swap equal to the new memory", 512 * mib, 2048 * mib, 2048 * mib, 2048 * mib, false},
		{"custom swap too small", 512 * mib, 600 * mib, 2048 * mib, 0, true},
		{"unlimited swap, the limit removed", 512 * mib, -1, 0, 0, false},
		{"unlimited swap, no limit before", 0, -1, 512 * mib, -1, false},
	}
	for _, c := range cases {
		got, err := memorySwap(c.baseMemory, c.baseSwap, c.memory)
		if c.fails {
			var fe *FieldError
			if !errors.As(err, &fe) || fe.Field != "resources.memory" {
				t.Errorf("%s: err %v, want a FieldError on resources.memory", c.name, err)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Errorf("%s: %d %v, want %d", c.name, got, err, c.want)
		}
	}
}

func TestValidateResources(t *testing.T) {
	cases := map[string]struct {
		r          Resources
		field, msg string // msg is part of the message of the FieldError
	}{
		"none":                        {Resources{}, "", ""},
		"at the minimum":              {Resources{Memory: minMemory, CPUs: 0.01}, "", ""},
		"memory too low":              {Resources{Memory: minMemory - 1}, "resources.memory", "at least 6 MiB"},
		"memory negative":             {Resources{Memory: -1}, "resources.memory", "negative"},
		"memory at the maximum":       {Resources{Memory: 1 << 50}, "", ""}, // 1 PiB
		"memory above the maximum":    {Resources{Memory: 1<<50 + 1}, "resources.memory", "1 PiB"},
		"memory far above":            {Resources{Memory: math.MaxInt64}, "resources.memory", "1 PiB"}, // 2 x memory would overflow
		"cpus too low":                {Resources{CPUs: 0.004}, "resources.cpus", "at least 0.01"},
		"cpus negative":               {Resources{CPUs: -0.5}, "resources.cpus", "negative"},
		"cpus rounded to the minimum": {Resources{CPUs: 0.0099999999}, "", ""}, // 10 000 000 NanoCPUs after rounding
		"cpus rounded to nothing":     {Resources{CPUs: 1e-12}, "resources.cpus", "at least 0.01"},
		"cpus at the maximum":         {Resources{CPUs: 1_000_000}, "", ""},
		"cpus above the maximum":      {Resources{CPUs: 1_000_001}, "resources.cpus", "more than 1000000"},
		"cpus 1e12":                   {Resources{CPUs: 1e12}, "resources.cpus", "more than 1000000"}, // beyond int64 as NanoCPUs
		"cpus 1e300":                  {Resources{CPUs: 1e300}, "resources.cpus", "more than 1000000"},
		// NaN and Inf cannot come from JSON; Validate does not rely on that
		"cpus NaN":  {Resources{CPUs: math.NaN()}, "resources.cpus", "must be a number"},
		"cpus +Inf": {Resources{CPUs: math.Inf(1)}, "resources.cpus", "must be a number"},
		"cpus -Inf": {Resources{CPUs: math.Inf(-1)}, "resources.cpus", "must be a number"},
	}
	for name, c := range cases {
		s := validSpec()
		s.Resources = c.r
		err := s.Validate()
		var fe *FieldError
		switch {
		case c.field == "" && err != nil:
			t.Errorf("%s: %v", name, err)
		case c.field != "" && (!errors.As(err, &fe) || fe.Field != c.field || !strings.Contains(fe.Message, c.msg)):
			t.Errorf("%s: err %v, want a FieldError on %s with %q", name, err, c.field, c.msg)
		}
	}
}

func TestValidateHost(t *testing.T) {
	s := validSpec()
	s.Resources.CPUs = 4.5
	var fe *FieldError
	if err := s.ValidateHost(4); !errors.As(err, &fe) || fe.Field != "resources.cpus" {
		t.Fatalf("4.5 of 4 cores: %v", err)
	}
	if err := s.ValidateHost(0); err != nil { // the host's count is unknown: nothing to check against
		t.Fatalf("unknown host: %v", err)
	}
	s.Resources.CPUs = 4
	if err := s.ValidateHost(4); err != nil {
		t.Fatalf("4 of 4 cores: %v", err)
	}
}
