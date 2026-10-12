package containers

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/sse"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss/sdk"
)

// LogLine is one line of a container's output.
type LogLine struct {
	Stream string `json:"stream"` // stdout or stderr
	Time   string `json:"time"`   // Docker's timestamp (RFC 3339 with nanoseconds)
	Line   string `json:"line"`
}

const (
	defaultTail = 500
	maxTail     = 5000
	maxLine     = 64 << 10 // a longer "line" is sent in parts
	logsKey     = "logs"
	statsKey    = "stats"
)

type streamTarget struct {
	id      string
	tail    int
	tty     bool
	running bool
}

// prepareStream checks the container before the SSE stream starts, so an unknown container is a proper 404. What the
// stream needs goes into c.Locals as a value: the stream handler runs after this one has returned (see sdk.SSE), and
// the id in it comes from Docker's answer, not from the request's buffers.
func (s *Service) prepareStream(key string) fiber.Handler {
	return func(c fiber.Ctx) error {
		name, err := containerID(c)
		if err != nil {
			return err
		}
		tail := defaultTail
		if q := c.Query("tail"); q != "" {
			n, err := strconv.Atoi(q)
			if err != nil || n < 0 {
				return sdk.NewError(fiber.StatusBadRequest, "invalid_request", "tail must be a number").WithDetails(map[string]any{"field": "tail"})
			}
			tail = min(n, maxTail)
		}
		res, err := s.Docker.ContainerInspect(c.Context(), name, client.ContainerInspectOptions{})
		if err != nil {
			return dockerapi.Error(err)
		}
		ct := res.Container
		// a restarting container has no statistics either: the daemon answers a subscription with empty records
		running := ct.State != nil && ct.State.Running && !ct.State.Restarting
		c.Locals(key, streamTarget{id: ct.ID, tail: tail, tty: ct.Config != nil && ct.Config.Tty, running: running})
		return c.Next()
	}
}

// streamContext ends when the browser leaves the stream.
func streamContext(stream *sse.Stream) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-stream.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (s *Service) logs(c fiber.Ctx, stream *sse.Stream) error {
	target := c.Locals(logsKey).(streamTarget) // the only use of c, at the start (see sdk.SSE)
	ctx, cancel := streamContext(stream)
	defer cancel()
	rc, err := s.Docker.ContainerLogs(ctx, target.id, client.ContainerLogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: true, Tail: strconv.Itoa(target.tail),
	})
	if err != nil {
		_ = stream.Event(sse.Event{Name: "error", Data: map[string]string{"message": dockerapi.Message(err)}})
		return nil
	}
	defer rc.Close()
	emit := func(name, line string) error {
		ts, text := splitTimestamp(line)
		return stream.Event(sse.Event{Name: "line", Data: LogLine{Stream: name, Time: ts, Line: text}})
	}
	// without a TTY every Write of the copy is one frame of Docker's multiplexed stream
	out := &lineWriter{stream: "stdout", frames: !target.tty, emit: emit}
	errw := &lineWriter{stream: "stderr", frames: !target.tty, emit: emit}
	var copyErr error
	if target.tty {
		_, copyErr = io.Copy(out, rc)
	} else {
		_, copyErr = stdcopy.StdCopy(out, errw, rc)
	}
	out.flush()
	errw.flush()
	switch {
	case ctx.Err() != nil || stream.Err() != nil: // the browser left (a failed read or write is its doing)
	case copyErr != nil: // e.g. the daemon's "Error grabbing logs" frame, or a broken connection: not a normal end
		_ = stream.Event(sse.Event{Name: "error", Data: map[string]string{"message": dockerapi.Message(copyErr)}})
	default: // the container ended
		_ = stream.Event(sse.Event{Name: "end", Data: "{}"})
	}
	return nil
}

// lineWriter cuts what Docker writes into lines.
type lineWriter struct {
	stream string
	// frames is set when every Write is one frame of Docker's multiplexed stream (a container without a TTY). The daemon
	// cuts a line of more than 16 KiB into parts, sends each part as a frame with its own timestamp, and ends only the
	// last one with a line feed: the timestamp at the start of a frame that continues a line is dropped.
	frames bool
	buf    []byte
	emit   func(stream, line string) error
}

func (w *lineWriter) Write(p []byte) (int, error) {
	n := len(p)
	if w.frames && len(w.buf) > 0 {
		p = dropTimestamp(p)
	}
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSuffix(string(w.buf[:i]), "\r")
		w.buf = w.buf[i+1:]
		if err := w.emit(w.stream, line); err != nil {
			return 0, err
		}
	}
	if len(w.buf) > maxLine {
		cut := runeBoundary(w.buf)
		if err := w.emit(w.stream, string(w.buf[:cut])); err != nil {
			return 0, err
		}
		w.buf = w.buf[:copy(w.buf, w.buf[cut:])] // what is left is the start of a rune that the next write completes
	}
	return n, nil
}

// runeBoundary is the length of the longest prefix of b that does not end inside a UTF-8 rune.
func runeBoundary(b []byte) int {
	for i := len(b) - 1; i >= 0 && i >= len(b)-utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i]) {
			if utf8.FullRune(b[i:]) {
				return len(b)
			}
			return i
		}
	}
	return len(b)
}

// dropTimestamp returns p without the timestamp at its start, if it has one.
func dropTimestamp(p []byte) []byte {
	first, rest, ok := bytes.Cut(p, []byte{' '})
	if ok && isTimestamp(string(first)) {
		return rest
	}
	return p
}

func (w *lineWriter) flush() {
	if len(w.buf) > 0 {
		_ = w.emit(w.stream, strings.TrimSuffix(string(w.buf), "\r"))
		w.buf = nil
	}
}

// splitTimestamp separates Docker's timestamp (Timestamps: true) from the line.
func splitTimestamp(line string) (ts, text string) {
	first, rest, _ := strings.Cut(line, " ")
	if !isTimestamp(first) {
		return "", line
	}
	return first, rest
}

func isTimestamp(s string) bool {
	_, err := time.Parse(time.RFC3339Nano, s)
	return err == nil
}
