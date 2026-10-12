package containers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// sseEvents reads an SSE body: the event names with their data.
func sseEvents(t *testing.T, body io.Reader) (names []string, data []string) {
	t.Helper()
	sc := bufio.NewScanner(body)
	name := ""
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			names, data = append(names, name), append(data, strings.TrimPrefix(line, "data: "))
		}
	}
	return names, data
}

func TestLineWriter(t *testing.T) {
	var got []string
	w := &lineWriter{stream: "stdout", emit: func(stream, line string) error { got = append(got, stream+":"+line); return nil }}
	_, _ = w.Write([]byte("one\ntw"))
	_, _ = w.Write([]byte("o\r\nthree"))
	w.flush()
	if strings.Join(got, "|") != "stdout:one|stdout:two|stdout:three" {
		t.Fatalf("lines %v", got)
	}
}

// A "line" without a line feed that grows beyond maxLine is sent in parts, and a failing emit (the browser has left) stops
// the copy from Docker.
func TestLineWriterLongLinesAndFailure(t *testing.T) {
	var lens []int
	w := &lineWriter{stream: "stdout", emit: func(_, line string) error { lens = append(lens, len(line)); return nil }}
	_, _ = w.Write(bytes.Repeat([]byte("x"), maxLine))
	if len(lens) != 0 {
		t.Fatalf("up to maxLine bytes wait for their line feed: %v", lens)
	}
	_, _ = w.Write([]byte("yy"))
	_, _ = w.Write([]byte("zz\n"))
	if len(lens) != 2 || lens[0] != maxLine+2 || lens[1] != 2 {
		t.Fatalf("parts %v", lens)
	}

	gone := &lineWriter{stream: "stdout", emit: func(_, _ string) error { return io.ErrClosedPipe }}
	if _, err := gone.Write([]byte("one\n")); err != io.ErrClosedPipe {
		t.Fatalf("write error %v", err)
	}
}

// The daemon cuts a line of more than 16 KiB into parts: without a TTY each part is a frame with its own timestamp and
// only the last one ends with a line feed. The timestamp of a frame that continues a line is dropped; with a TTY there are
// no frame boundaries, and the text stays as it is.
func TestLineWriterJoinsTheFramesOfOneLine(t *testing.T) {
	const ts0, ts1, ts2 = "2026-10-10T12:00:00Z", "2026-10-10T12:00:01Z", "2026-10-10T12:00:02Z"
	for _, tc := range []struct {
		name   string
		frames bool
		want   []string
	}{
		{"frames", true, []string{ts0 + " aaabbb", ts2 + " next"}},
		{"tty", false, []string{ts0 + " aaa" + ts1 + " bbb", ts2 + " next"}},
	} {
		var got []string
		w := &lineWriter{stream: "stdout", frames: tc.frames, emit: func(_, line string) error { got = append(got, line); return nil }}
		_, _ = w.Write([]byte(ts0 + " aaa"))
		second := []byte(ts1 + " bbb\n")
		if n, err := w.Write(second); n != len(second) || err != nil { // io.Copy and StdCopy check the count
			t.Fatalf("%s: Write returned %d, %v for %d bytes", tc.name, n, err, len(second))
		}
		_, _ = w.Write([]byte(ts2 + " next\n"))
		if strings.Join(got, "|") != strings.Join(tc.want, "|") {
			t.Errorf("%s: lines %q, want %q", tc.name, got, tc.want)
		}
	}

	// only the start of a continuation is looked at; text without a timestamp stays, and so does a timestamp that
	// starts a line
	var got []string
	w := &lineWriter{stream: "stdout", frames: true, emit: func(_, line string) error { got = append(got, line); return nil }}
	_, _ = w.Write([]byte(ts0 + " aaa"))
	_, _ = w.Write([]byte("plain\n"))
	_, _ = w.Write([]byte(ts1 + " " + ts2 + " x\n"))
	if strings.Join(got, "|") != ts0+" aaaplain|"+ts1+" "+ts2+" x" {
		t.Errorf("lines %q", got)
	}
}

// The cut of a very long line must not split a UTF-8 rune: the daemon's parts end anywhere, also inside a rune.
func TestLineWriterDoesNotSplitRunes(t *testing.T) {
	euro := "€" // e2 82 ac
	for _, frames := range []bool{true, false} {
		var got []string
		w := &lineWriter{stream: "stdout", frames: frames, emit: func(_, line string) error { got = append(got, line); return nil }}
		_, _ = w.Write([]byte(strings.Repeat("x", maxLine) + euro[:2]))
		if len(got) != 1 || got[0] != strings.Repeat("x", maxLine) {
			t.Fatalf("frames %v: first cut %d parts, %q...", frames, len(got), got)
		}
		rest := euro[2:] + "BBB\n"
		if frames {
			rest = "2026-10-10T12:00:01Z " + rest
		}
		_, _ = w.Write([]byte(rest))
		if len(got) != 2 || got[1] != euro+"BBB" {
			t.Fatalf("frames %v: the rune and the rest of the line: %q", frames, got[1:])
		}
	}

	// a rune that is complete at the end is not held back
	var got []string
	w := &lineWriter{stream: "stdout", emit: func(_, line string) error { got = append(got, line); return nil }}
	_, _ = w.Write([]byte(strings.Repeat("x", maxLine) + euro))
	if len(got) != 1 || got[0] != strings.Repeat("x", maxLine)+euro || len(w.buf) != 0 {
		t.Fatalf("complete rune: %d parts, %d bytes left", len(got), len(w.buf))
	}
}

func TestRuneBoundary(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", 0},
		{"ascii", "abc", 3},
		{"complete 3-byte rune", "ab€", 5},
		{"complete 4-byte rune", "ab😀", 6},
		{"2 of 3 bytes", "ab€"[:4], 2},
		{"1 of 3 bytes", "ab€"[:3], 2},
		{"3 of 4 bytes", "ab😀"[:5], 2},
		{"only continuation bytes (invalid)", "ab\x80\x80\x80\x80\x80", 7},
		{"a start byte followed by ASCII (invalid)", "ab\xe2(", 4},
	} {
		if got := runeBoundary([]byte(tc.in)); got != tc.want {
			t.Errorf("%s: %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestSplitTimestamp(t *testing.T) {
	ts, line := splitTimestamp("2026-10-10T12:00:00.123456789Z hello world")
	if ts != "2026-10-10T12:00:00.123456789Z" || line != "hello world" {
		t.Fatalf("%q %q", ts, line)
	}
	if ts, line := splitTimestamp("no timestamp here"); ts != "" || line != "no timestamp here" {
		t.Fatalf("%q %q", ts, line)
	}
}

func logsFake(tty bool, out []byte, gotTail *string) *dockertest.Fake {
	return logsFakeReader(tty, bytes.NewReader(out), gotTail)
}

func logsFakeReader(tty bool, body io.Reader, gotTail *string) *dockertest.Fake {
	return &dockertest.Fake{
		ContainerInspectFn: func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
			return client.ContainerInspectResult{Container: container.InspectResponse{ID: id, Config: &container.Config{Tty: tty}}}, nil
		},
		ContainerLogsFn: func(_ context.Context, _ string, o client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
			*gotTail = o.Tail
			if !o.Follow || !o.Timestamps || !o.ShowStdout || !o.ShowStderr {
				return nil, io.ErrUnexpectedEOF
			}
			return io.NopCloser(body), nil
		},
	}
}

// frame is one frame of Docker's multiplexed stream (stdcopy has no writer): stream type, three zero bytes, the payload
// length as a big-endian uint32, the payload.
func frame(stream stdcopy.StdType, payload string) []byte {
	head := []byte{byte(stream), 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(head[4:], uint32(len(payload)))
	return append(head, payload...)
}

func TestLogs(t *testing.T) {
	var mux bytes.Buffer
	mux.Write(frame(stdcopy.Stdout, "2026-10-10T12:00:00Z ready\n"))
	mux.Write(frame(stdcopy.Stderr, "2026-10-10T12:00:01Z oops\n"))
	tail := ""
	resp := sdktest.Do(t, testApp(logsFake(false, mux.Bytes(), &tail)), sdktest.Request("GET", "/containers/web/logs", nil, anna))
	names, data := sseEvents(t, resp.Body)
	if strings.Join(names, ",") != "line,line,end" || tail != "500" {
		t.Fatalf("events %v, tail %q", names, tail)
	}
	var first, second LogLine
	_ = json.Unmarshal([]byte(data[0]), &first)
	_ = json.Unmarshal([]byte(data[1]), &second)
	if first != (LogLine{Stream: "stdout", Time: "2026-10-10T12:00:00Z", Line: "ready"}) || second.Stream != "stderr" || second.Line != "oops" {
		t.Fatalf("lines %+v %+v", first, second)
	}

	// a TTY gives one raw stream
	resp = sdktest.Do(t, testApp(logsFake(true, []byte("2026-10-10T12:00:00Z $ ls\n"), &tail)), sdktest.Request("GET", "/containers/web/logs?tail=99999", nil, anna))
	names, data = sseEvents(t, resp.Body)
	if len(names) != 2 || !strings.Contains(data[0], `"line":"$ ls"`) || tail != "5000" {
		t.Fatalf("tty: %v %v tail %q", names, data, tail)
	}
	if resp := sdktest.Do(t, testApp(logsFake(false, nil, &tail)), sdktest.Request("GET", "/containers/web/logs?tail=x", nil, anna)); resp.StatusCode != 400 {
		t.Fatalf("invalid tail: %d", resp.StatusCode)
	}
}

// A failure in the middle of the logs is an error event and no end: the daemon reports it as a frame of the stream type 3
// ("Error grabbing logs"), and a connection can break. What came before is delivered, what follows a system error is not.
func TestLogsFailuresAreErrors(t *testing.T) {
	var systemErr bytes.Buffer
	systemErr.Write(frame(stdcopy.Stdout, "2026-10-10T12:00:00Z ready\n"))
	systemErr.Write(frame(stdcopy.Systemerr, "Error grabbing logs: boom"))
	systemErr.Write(frame(stdcopy.Stdout, "2026-10-10T12:00:02Z never seen\n"))
	reset := iotest.ErrReader(errors.New("connection reset"))
	for _, tc := range []struct {
		name string
		tty  bool
		body io.Reader
		want string
	}{
		{"system error frame", false, &systemErr, "boom"},
		{"broken connection", false, io.MultiReader(bytes.NewReader(frame(stdcopy.Stdout, "2026-10-10T12:00:00Z ready\n")), reset), "connection reset"},
		{"broken connection with a TTY", true, io.MultiReader(strings.NewReader("2026-10-10T12:00:00Z ready\n"), reset), "connection reset"},
	} {
		tail := ""
		resp := sdktest.Do(t, testApp(logsFakeReader(tc.tty, tc.body, &tail)), sdktest.Request("GET", "/containers/web/logs", nil, anna))
		names, data := sseEvents(t, resp.Body)
		if strings.Join(names, ",") != "line,error" || !strings.Contains(data[1], tc.want) || !strings.Contains(data[0], `"line":"ready"`) {
			t.Errorf("%s: events %v %v", tc.name, names, data)
		}
	}
}

// Without a TTY a line of more than 16 KiB arrives as several frames, each with its own timestamp: one line, with the
// timestamp of its first part. A line of the other stream in between is a line of its own.
func TestLogsJoinsTheFramesOfALongLine(t *testing.T) {
	var mux bytes.Buffer
	mux.Write(frame(stdcopy.Stdout, "2026-10-10T12:00:00.1Z aaa"))
	mux.Write(frame(stdcopy.Stderr, "2026-10-10T12:00:00.15Z err\n"))
	mux.Write(frame(stdcopy.Stdout, "2026-10-10T12:00:00.2Z bbb"))
	mux.Write(frame(stdcopy.Stdout, "2026-10-10T12:00:00.3Z ccc\n"))
	mux.Write(frame(stdcopy.Stdout, "2026-10-10T12:00:01Z next\n"))
	tail := ""
	resp := sdktest.Do(t, testApp(logsFake(false, mux.Bytes(), &tail)), sdktest.Request("GET", "/containers/web/logs", nil, anna))
	names, data := sseEvents(t, resp.Body)
	if strings.Join(names, ",") != "line,line,line,end" {
		t.Fatalf("events %v %v", names, data)
	}
	var lines [3]LogLine
	for i := range lines {
		_ = json.Unmarshal([]byte(data[i]), &lines[i])
	}
	want := [3]LogLine{
		{Stream: "stderr", Time: "2026-10-10T12:00:00.15Z", Line: "err"},
		{Stream: "stdout", Time: "2026-10-10T12:00:00.1Z", Line: "aaabbbccc"},
		{Stream: "stdout", Time: "2026-10-10T12:00:01Z", Line: "next"},
	}
	if lines != want {
		t.Fatalf("lines %+v, want %+v", lines, want)
	}
}

func TestLogsTail(t *testing.T) {
	for _, tc := range []struct {
		query, tail string
		status      int
	}{
		{"", "500", 200}, {"?tail=0", "0", 200}, {"?tail=20", "20", 200}, {"?tail=5000", "5000", 200}, {"?tail=5001", "5000", 200},
		{"?tail=-1", "", 400}, {"?tail=1.5", "", 400}, {"?tail=all", "", 400},
	} {
		tail := ""
		resp := sdktest.Do(t, testApp(logsFake(false, nil, &tail)), sdktest.Request("GET", "/containers/web/logs"+tc.query, nil, anna))
		_, _ = sseEvents(t, resp.Body)
		if resp.StatusCode != tc.status || tail != tc.tail {
			t.Errorf("%q: status %d, tail %q; want %d, %q", tc.query, resp.StatusCode, tail, tc.status, tc.tail)
		}
	}
}

// Both stream routes put the id into the daemon's URL path like the others do: ids that the client's path.Join would clean
// into another endpoint never reach Docker, and the container the stream follows is the one inspect found.
func TestStreamsRefuseUnsafeIds(t *testing.T) {
	var calls []string
	f := &dockertest.Fake{ContainerInspectFn: func(_ context.Context, id string, _ client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		calls = append(calls, strings.Clone(id))
		return client.ContainerInspectResult{Container: container.InspectResponse{ID: id, State: &container.State{}}}, nil
	}}
	app := testApp(f)
	for _, id := range []string{".", "..", "..%2Fimages%2Fx", ".hidden", "-web", "web%2F..", "we%20b"} {
		for _, suffix := range []string{"/logs", "/stats"} {
			target := "/containers/" + id + suffix
			resp := sdktest.Do(t, app, sdktest.Request("GET", target, nil, anna))
			if raw := readAll(t, resp); resp.StatusCode != 400 || !strings.Contains(raw, "invalid_request") || !strings.Contains(raw, `"field":"id"`) {
				t.Errorf("GET %s: %d %s", target, resp.StatusCode, raw)
			}
		}
	}
	if len(calls) != 0 {
		t.Fatalf("Docker must not be called for these ids, was called: %q", calls)
	}
	for _, suffix := range []string{"/logs", "/stats"} {
		resp := sdktest.Do(t, app, sdktest.Request("GET", "/containers/web"+suffix, nil, anna))
		_, _ = sseEvents(t, resp.Body)
		if resp.StatusCode != 200 || len(calls) == 0 || calls[len(calls)-1] != "web" {
			t.Errorf("GET /containers/web%s: %d, inspected %q", suffix, resp.StatusCode, calls)
		}
	}
}

// An unknown container is a 404 before the stream starts, not an error event inside a 200.
func TestStreamsOfUnknownContainerAreNotFound(t *testing.T) {
	f := &dockertest.Fake{ContainerInspectFn: func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
		return client.ContainerInspectResult{}, cerrdefs.ErrNotFound
	}}
	for _, suffix := range []string{"/logs", "/stats"} {
		resp := sdktest.Do(t, testApp(f), sdktest.Request("GET", "/containers/gone"+suffix, nil, anna))
		if raw := readAll(t, resp); resp.StatusCode != 404 || !strings.Contains(raw, "not_found") {
			t.Errorf("GET %s: %d %s", suffix, resp.StatusCode, raw)
		}
	}
}
