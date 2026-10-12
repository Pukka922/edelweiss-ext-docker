package console

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
)

// serve runs the console route on a real listener (WebSockets need one) and returns its ws:// base URL.
func serve(t *testing.T, d dockerapi.Docker) string {
	t.Helper()
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, d)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.ShutdownWithTimeout(2 * time.Second) })
	return "ws://" + ln.Addr().String()
}

// shell is a fake exec: it upper-cases each input line, ends on "exit" or when its input ends, and reports what happened.
type shell struct {
	mu         sync.Mutex
	created    client.ExecCreateOptions
	resized    []client.ExecResizeOptions
	failWrites bool     // set before the connect: the input of the browser cannot be written to the exec
	events     []string // what the browser's side of the connection to the exec did: "write <bytes>", "close-write"

	closed      chan struct{} // closed when the shell ended without "exit": its input ended or the connection was closed
	stdinClosed chan struct{} // closed by the first CloseWrite on the connection to the exec
	stdinOnce   sync.Once
}

// execConn is the backend end of the fake exec, with the half-close (CloseWrite) a connection to a real daemon has and
// that net.Pipe lacks: it ends the input of the fake shell and records the call.
type execConn struct {
	net.Conn
	sh       *shell
	endInput func()
}

func (c *execConn) Write(p []byte) (int, error) {
	c.sh.mu.Lock()
	c.sh.events = append(c.sh.events, "write "+string(p))
	fail := c.sh.failWrites
	c.sh.mu.Unlock()
	if fail {
		return 0, errors.New("write: broken pipe")
	}
	return c.Conn.Write(p)
}

func (c *execConn) CloseWrite() error {
	c.sh.mu.Lock()
	c.sh.events = append(c.sh.events, "close-write")
	c.sh.mu.Unlock()
	c.sh.stdinOnce.Do(func() { close(c.sh.stdinClosed) })
	c.endInput()
	return nil
}

func (s *shell) fake() *dockertest.Fake {
	s.closed = make(chan struct{})
	s.stdinClosed = make(chan struct{})
	return &dockertest.Fake{
		ExecCreateFn: func(_ context.Context, id string, o client.ExecCreateOptions) (client.ExecCreateResult, error) {
			if id == "stopped" {
				return client.ExecCreateResult{}, errors.New("Error response from daemon: container stopped is not running")
			}
			s.mu.Lock()
			s.created = o
			s.mu.Unlock()
			return client.ExecCreateResult{ID: "exec1"}, nil
		},
		ExecAttachFn: func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error) {
			backend, daemon := net.Pipe()
			go func() {
				defer daemon.Close()
				sc := bufio.NewScanner(daemon)
				for sc.Scan() {
					if sc.Text() == "exit" {
						return
					}
					if _, err := daemon.Write([]byte(strings.ToUpper(sc.Text()) + "\r\n")); err != nil {
						return
					}
				}
				close(s.closed) // the shell went away without "exit"
			}()
			conn := &execConn{Conn: backend, sh: s, endInput: func() { _ = daemon.SetReadDeadline(time.Now()) }}
			return client.ExecAttachResult{HijackedResponse: client.NewHijackedResponse(conn, "")}, nil
		},
		ExecResizeFn: func(_ context.Context, _ string, o client.ExecResizeOptions) (client.ExecResizeResult, error) {
			s.mu.Lock()
			s.resized = append(s.resized, o)
			s.mu.Unlock()
			return client.ExecResizeResult{}, nil
		},
		ExecInspectFn: func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{Running: false, ExitCode: 3}, nil
		},
	}
}

func dial(t *testing.T, url string) *fws.Conn {
	t.Helper()
	c, _, err := fws.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	return c
}

// readOutput reads one frame of output and fails unless it is want.
func readOutput(t *testing.T, c *fws.Conn, want string) {
	t.Helper()
	mt, out, err := c.ReadMessage()
	if err != nil || mt != fws.BinaryMessage || string(out) != want {
		t.Fatalf("output %d %q %v, want %q", mt, out, err, want)
	}
}

// readExit reads the exit frame and the close frame after it, and returns the exit code.
func readExit(t *testing.T, c *fws.Conn) int {
	t.Helper()
	mt, msg, err := c.ReadMessage()
	var exit struct {
		Type string `json:"type"`
		Code int    `json:"code"`
	}
	if err != nil || mt != fws.TextMessage || json.Unmarshal(msg, &exit) != nil || exit.Type != "exit" {
		t.Fatalf("exit frame %d %q %v", mt, msg, err)
	}
	if _, _, err := c.ReadMessage(); !fws.IsCloseError(err, fws.CloseNormalClosure) {
		t.Fatalf("close: %v", err)
	}
	return exit.Code
}

func TestConsole(t *testing.T) {
	sh := &shell{}
	c := dial(t, serve(t, sh.fake())+"/containers/web/console?cmd="+"bash%20-l")
	if err := c.WriteMessage(fws.BinaryMessage, []byte("hello\n")); err != nil {
		t.Fatal(err)
	}
	mt, out, err := c.ReadMessage()
	if err != nil || mt != fws.BinaryMessage || string(out) != "HELLO\r\n" {
		t.Fatalf("output %d %q %v", mt, out, err)
	}
	_ = c.WriteMessage(fws.TextMessage, []byte(`{"type":"resize","cols":100,"rows":30}`))
	_ = c.WriteMessage(fws.BinaryMessage, []byte("exit\n"))
	mt, msg, err := c.ReadMessage()
	var exit struct {
		Type string `json:"type"`
		Code int    `json:"code"`
	}
	if err != nil || mt != fws.TextMessage || json.Unmarshal(msg, &exit) != nil || exit.Type != "exit" || exit.Code != 3 {
		t.Fatalf("exit frame %d %q %v", mt, msg, err)
	}
	if _, _, err := c.ReadMessage(); !fws.IsCloseError(err, fws.CloseNormalClosure) {
		t.Fatalf("close: %v", err)
	}
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if strings.Join(sh.created.Cmd, " ") != "bash -l" || !sh.created.TTY || !sh.created.AttachStdin {
		t.Fatalf("exec %+v", sh.created)
	}
	if len(sh.resized) != 1 || sh.resized[0].Width != 100 || sh.resized[0].Height != 30 {
		t.Fatalf("resize %+v", sh.resized)
	}
}

// When the browser leaves, the input of the exec ends: ^D (a shell at its prompt exits on it; a TTY exec does not see the
// close of its stdin) and then CloseWrite. The shell ends by itself, well before the 2 s the handler would wait for it,
// and not only because the connection is closed in the end.
func TestConsoleClosesWhenTheBrowserLeaves(t *testing.T) {
	sh := &shell{}
	c := dial(t, serve(t, sh.fake())+"/containers/web/console")
	_ = c.WriteMessage(fws.BinaryMessage, []byte("ls\n"))
	if _, _, err := c.ReadMessage(); err != nil {
		t.Fatal(err)
	}
	sh.mu.Lock()
	cmd := strings.Join(sh.created.Cmd, " ")
	sh.mu.Unlock()
	if cmd != "/bin/sh" {
		t.Fatalf("default command %q", cmd)
	}
	c.Close()
	select {
	case <-sh.stdinClosed:
	case <-time.After(time.Second):
		t.Fatal("stdin of the exec was not closed after the browser left")
	}
	select {
	case <-sh.closed:
	case <-time.After(time.Second):
		t.Fatal("the shell stayed alive after its stdin was closed")
	}
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if want := []string{"write ls\n", "write \x04", "close-write"}; !slices.Equal(sh.events, want) {
		t.Fatalf("what the exec saw: %q, want %q", sh.events, want)
	}
}

func TestConsoleErrors(t *testing.T) {
	sh := &shell{}
	base := serve(t, sh.fake())
	for path, want := range map[string]string{
		"/containers/stopped/console":             "is not running",
		"/containers/web/console?cmd=%27unclosed": "invalid",
	} {
		c := dial(t, base+path)
		mt, msg, err := c.ReadMessage()
		if err != nil || mt != fws.TextMessage || !strings.Contains(string(msg), `"type":"error"`) || !strings.Contains(string(msg), want) {
			t.Errorf("%s: %d %q %v", path, mt, msg, err)
		}
	}
}

// A frame of the browser is read whole into memory: one above 1 MiB ends the session with close code 1009, and
// the shell gets its stdin closed.
func TestConsoleRefusesHugeFrames(t *testing.T) {
	sh := &shell{}
	c := dial(t, serve(t, sh.fake())+"/containers/web/console")
	_ = c.WriteMessage(fws.BinaryMessage, []byte("ls\n"))
	readOutput(t, c, "LS\r\n")
	// the error of the write is ignored: the server may stop reading before it has taken the whole frame
	_ = c.WriteMessage(fws.BinaryMessage, make([]byte, 1<<20+1))
	if _, _, err := c.ReadMessage(); !fws.IsCloseError(err, fws.CloseMessageTooBig) {
		t.Fatalf("read after a frame over the limit: %v", err)
	}
	select {
	case <-sh.stdinClosed:
	case <-time.After(2 * time.Second):
		t.Fatal("stdin of the exec was not closed after the session ended")
	}
}

// Sizes the kernel cannot take (its winsize is 16 bit), zero and what is no resize at all never reach Docker.
func TestConsoleIgnoresBadResizes(t *testing.T) {
	sh := &shell{}
	c := dial(t, serve(t, sh.fake())+"/containers/web/console")
	for _, frame := range []string{
		`{"type":"resize","cols":0,"rows":24}`,
		`{"type":"resize","cols":80,"rows":0}`,
		`{"type":"resize","cols":65536,"rows":24}`,
		`{"type":"resize","cols":80,"rows":65536}`,
		`{"type":"resize","cols":99999999999999,"rows":24}`,
		`{"type":"resize","cols":-1,"rows":24}`,
		`{"type":"resize","cols":"80","rows":24}`,
		`{"type":"resize","cols":80`,
		`not json`,
		`{"type":"scroll","cols":80,"rows":24}`,
	} {
		if err := c.WriteMessage(fws.TextMessage, []byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	// the frames are handled in order: once the next input is answered, all of them have been dealt with
	_ = c.WriteMessage(fws.BinaryMessage, []byte("ping\n"))
	readOutput(t, c, "PING\r\n")
	sh.mu.Lock()
	if len(sh.resized) != 0 {
		t.Fatalf("resized by bad frames: %+v", sh.resized)
	}
	sh.mu.Unlock()

	_ = c.WriteMessage(fws.TextMessage, []byte(`{"type":"resize","cols":65535,"rows":1}`))
	_ = c.WriteMessage(fws.BinaryMessage, []byte("pong\n"))
	readOutput(t, c, "PONG\r\n")
	sh.mu.Lock()
	defer sh.mu.Unlock()
	if len(sh.resized) != 1 || sh.resized[0].Width != 65535 || sh.resized[0].Height != 1 {
		t.Fatalf("resize %+v", sh.resized)
	}
}

// The input side can end before the command does (here: the exec takes no more input). The browser is still there, so it
// gets the exit frame and the close frame once the command ends.
func TestConsoleReportsTheEndAfterAFailedWrite(t *testing.T) {
	sh := &shell{failWrites: true}
	c := dial(t, serve(t, sh.fake())+"/containers/web/console")
	_ = c.WriteMessage(fws.BinaryMessage, []byte("hello\n"))
	if code := readExit(t, c); code != 3 {
		t.Fatalf("exit code %d", code)
	}
}

// After the close frame the handler waits a moment for the browser's answer (its echo of the close frame) before it
// drops the connection, so the browser sees a clean close and not a reset.
func TestConsoleWaitsForTheCloseEcho(t *testing.T) {
	for _, tc := range []struct {
		name string
		echo bool
	}{{"the browser answers", true}, {"the browser stays silent", false}} {
		t.Run(tc.name, func(t *testing.T) {
			sh := &shell{}
			c := dial(t, serve(t, sh.fake())+"/containers/web/console")
			if !tc.echo {
				c.SetCloseHandler(func(int, string) error { return nil })
			}
			_ = c.WriteMessage(fws.BinaryMessage, []byte("exit\n"))
			readExit(t, c)
			start := time.Now()
			raw := c.UnderlyingConn()
			_ = raw.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := raw.Read(make([]byte, 1)); err != io.EOF {
				t.Fatalf("the connection was not closed by the server: %v", err)
			}
			waited := time.Since(start)
			switch {
			case tc.echo && waited > 500*time.Millisecond:
				t.Errorf("closed after %v: the echo of the close frame should end the wait", waited)
			case !tc.echo && waited < 700*time.Millisecond:
				t.Errorf("closed after %v: the server did not wait for the close echo", waited)
			}
		})
	}
}

// The exit code is looked up until the exec is reported as ended.
func TestExitCode(t *testing.T) {
	calls := 0
	d := &dockertest.Fake{ExecInspectFn: func(_ context.Context, id string, _ client.ExecInspectOptions) (client.ExecInspectResult, error) {
		calls++
		return client.ExecInspectResult{Running: calls <= 2, ExitCode: 5}, nil
	}}
	if got := exitCode(context.Background(), d, "exec1", time.Millisecond, time.Second); got != 5 || calls != 3 {
		t.Fatalf("exit code %d after %d calls, want 5 after 3", got, calls)
	}
}

// Without an answer the exit code is -1: a daemon that does not answer must not hold the exit frame back, nor an exec
// that never ends, nor an error.
func TestExitCodeGivesUp(t *testing.T) {
	t.Run("daemon hangs", func(t *testing.T) {
		d := &dockertest.Fake{ExecInspectFn: func(ctx context.Context, _ string, _ client.ExecInspectOptions) (client.ExecInspectResult, error) {
			select {
			case <-ctx.Done():
				return client.ExecInspectResult{}, ctx.Err()
			case <-time.After(10 * time.Second):
				return client.ExecInspectResult{ExitCode: 1}, nil
			}
		}}
		start := time.Now()
		if got := exitCode(context.Background(), d, "exec1", time.Millisecond, 50*time.Millisecond); got != -1 {
			t.Fatalf("exit code %d", got)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("took %v for a 50 ms limit", elapsed)
		}
	})
	t.Run("exec never ends", func(t *testing.T) {
		calls := 0
		d := &dockertest.Fake{ExecInspectFn: func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
			calls++
			return client.ExecInspectResult{Running: true}, nil
		}}
		if got := exitCode(context.Background(), d, "exec1", time.Millisecond, 10*time.Second); got != -1 || calls != exitPollTries {
			t.Fatalf("exit code %d after %d calls, want -1 after %d", got, calls, exitPollTries)
		}
	})
	t.Run("inspect fails", func(t *testing.T) {
		d := &dockertest.Fake{ExecInspectFn: func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error) {
			return client.ExecInspectResult{}, errors.New("no such exec")
		}}
		if got := exitCode(context.Background(), d, "exec1", time.Millisecond, time.Second); got != -1 {
			t.Fatalf("exit code %d", got)
		}
	})
}

// The :id of the route goes into the URL path of the daemon request, where the client's path.Join would clean "." and
// ".." into another endpoint; Fiber routes on the raw path, so such an id reaches the handler. Like every container
// route, the console refuses it with a plain 400 before the upgrade, and nothing reaches Docker.
func TestConsoleRefusesUnsafeIDs(t *testing.T) {
	var mu sync.Mutex
	var created []string
	d := &dockertest.Fake{ExecCreateFn: func(_ context.Context, id string, _ client.ExecCreateOptions) (client.ExecCreateResult, error) {
		mu.Lock()
		created = append(created, id)
		mu.Unlock()
		return client.ExecCreateResult{}, errors.New("must not be called")
	}}
	base := serve(t, d)
	for _, id := range []string{".", "..", "..%2Fimages%2Fx", ".hidden", "-web", "web%2F..", "we%20b"} {
		c, resp, err := fws.DefaultDialer.Dial(base+"/containers/"+id+"/console", nil)
		if err == nil {
			c.Close()
			t.Errorf("%s: the upgrade succeeded", id)
			continue
		}
		if resp == nil {
			t.Errorf("%s: no answer: %v", id, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 400 || !strings.Contains(string(body), "invalid_request") || !strings.Contains(string(body), `"field":"id"`) {
			t.Errorf("%s: %d %s", id, resp.StatusCode, body)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(created) != 0 {
		t.Fatalf("Docker must not be called for these ids, ExecCreate ran for %q", created)
	}
}
