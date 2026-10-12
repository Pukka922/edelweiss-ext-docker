// Package console runs a command (a shell by default) in a container for the browser: docker exec with a TTY over a
// WebSocket.
package console

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/gofiber/contrib/v3/websocket"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/shellwords"
)

const (
	// maxFrame is the largest message of the browser (typed or pasted input): a frame is read whole into memory, and a
	// larger one ends the session with close code 1009.
	maxFrame = 1 << 20
	// maxTermSize is the largest number of columns or rows: the winsize of the kernel is 16 bit.
	maxTermSize = 1<<16 - 1
	// endOfTransmission is ^D, the end of input of a terminal.
	endOfTransmission = 0x04
	// leaveWait is how long a command may take to end after the browser left and its stdin was closed.
	leaveWait = 2 * time.Second
	// writeTimeout is the deadline of the final frames: a browser that stopped reading must not hold the session.
	writeTimeout = 5 * time.Second
	// closeWait is how long the session waits for the browser's answer to the close frame before the connection is dropped.
	closeWait = time.Second

	// The exit code is asked for up to exitPollTries times, exitPollEvery apart, and all of it takes at most
	// exitPollTimeout, also when the daemon does not answer.
	exitPollEvery   = 50 * time.Millisecond
	exitPollTries   = 20
	exitPollTimeout = 5 * time.Second
)

// control is a message of the browser in a text frame.
type control struct {
	Type string `json:"type"` // "resize"
	Cols uint   `json:"cols"`
	Rows uint   `json:"rows"`
}

// Register adds GET /containers/:id/console. edelweissd checks the Origin of the upgrade before it forwards it.
func Register(r fiber.Router, d dockerapi.Docker) {
	r.Get("/containers/:id/console", checkID, websocket.New(func(c *websocket.Conn) { handle(c, d) }))
}

// checkID answers an id that is no container name or ID with dockerapi.InvalidID before the upgrade, like every
// container route (the id goes into the URL path of the daemon request, see dockerapi.SafeID).
func checkID(c fiber.Ctx) error {
	if !dockerapi.SafeID(c.Params("id")) {
		return dockerapi.InvalidID()
	}
	return c.Next()
}

// session is one console: the WebSocket, written by two goroutines.
type session struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *session) write(mt int, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(mt, data)
}

func (s *session) send(v any) {
	b, _ := json.Marshal(v)
	_ = s.write(websocket.TextMessage, b)
}

// close ends the session cleanly (close frame 1000); the connection stays open otherwise.
func (s *session) close(reason string) {
	_ = s.write(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, reason))
}

// limitWrites gives the writes after it a deadline; the final frames call it, so that a browser that stopped reading
// cannot hold the session.
func (s *session) limitWrites() {
	s.mu.Lock()
	defer s.mu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
}

// fail tells the browser why the command cannot start, closes the session and waits a moment for the browser's answer
// (it reads and drops what the browser still sends, so that closing the connection does not reset it). Only for the
// time before the read loop of the session runs.
func (s *session) fail(msg string) {
	s.limitWrites()
	s.send(map[string]string{"type": "error", "message": msg})
	s.close("error")
	_ = s.conn.SetReadDeadline(time.Now().Add(closeWait))
	for {
		if _, _, err := s.conn.ReadMessage(); err != nil {
			return
		}
	}
}

// finish tells the browser that the command ended and closes the session. The writes fail without harm when the
// browser has left already.
func (s *session) finish(code int) {
	s.limitWrites()
	s.send(map[string]any{"type": "exit", "code": code})
	s.close("exited")
}

// handle runs one console. The contrib Conn holds copies of the route parameters and the query (made at the upgrade,
// while Fiber's buffers were still valid), so c.Params and c.Query are safe to use here.
func handle(c *websocket.Conn, d dockerapi.Docker) {
	defer c.Close() // the contrib handler keeps the connection open otherwise
	c.SetReadLimit(maxFrame)
	s := &session{conn: c}
	cmd, err := shellwords.Split(c.Query("cmd", "/bin/sh"))
	if err != nil || len(cmd) == 0 {
		s.fail("the command is invalid")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ex, err := d.ExecCreate(ctx, c.Params("id"), client.ExecCreateOptions{
		TTY: true, AttachStdin: true, AttachStdout: true, AttachStderr: true, Cmd: cmd, Env: []string{"TERM=xterm-256color"},
	})
	if err != nil {
		s.fail(dockerapi.Message(err))
		return
	}
	att, err := d.ExecAttach(ctx, ex.ID, client.ExecAttachOptions{TTY: true})
	if err != nil {
		s.fail(dockerapi.Message(err))
		return
	}
	defer att.Close()

	outDone := make(chan struct{})
	go func() { // container → browser
		defer close(outDone)
		buf := make([]byte, 32<<10)
		for {
			n, err := att.Reader.Read(buf)
			if n > 0 {
				if werr := s.write(websocket.BinaryMessage, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	inDone := make(chan struct{})
	go func() { // browser → container
		defer close(inDone)
		for {
			mt, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if _, err := att.Conn.Write(data); err != nil {
					return
				}
			case websocket.TextMessage:
				var ctl control
				if json.Unmarshal(data, &ctl) == nil && ctl.Type == "resize" && validSize(ctl.Cols) && validSize(ctl.Rows) {
					_, _ = d.ExecResize(ctx, ex.ID, client.ExecResizeOptions{Height: ctl.Rows, Width: ctl.Cols})
				}
			}
		}
	}()

	select {
	case <-outDone: // the command ended
	case <-inDone: // the browser left (or its input cannot be written): end the input, so a shell ends instead of lingering
		hangUp(att.HijackedResponse)
		select {
		case <-outDone: // it ended; the browser may still be there, so it is told below
		case <-time.After(leaveWait):
			return // it goes on without a console; closing the connection (deferred) ends its streams
		}
	}
	s.finish(exitCode(ctx, d, ex.ID, exitPollEvery, exitPollTimeout))
	select { // the browser answers the close frame, and its answer ends the read loop
	case <-inDone:
	case <-time.After(closeWait):
	}
}

// hangUp ends the input of the exec when the browser is gone. For an exec with a TTY, the daemon does not pass the end
// of stdin on to the process (it does that only without a TTY), and a closed connection does not end the process
// either (checked against a daemon: the shell stays in the container), so the end of input is sent as a character: ^D
// makes a shell at its prompt exit. The half-close that follows ends the stream to the daemon.
func hangUp(att client.HijackedResponse) {
	_ = att.Conn.SetWriteDeadline(time.Now().Add(closeWait))
	_, _ = att.Conn.Write([]byte{endOfTransmission})
	_ = att.CloseWrite()
}

// validSize reports whether n can be the number of columns or rows of a terminal.
func validSize(n uint) bool { return n > 0 && n <= maxTermSize }

// exitCode asks for the exit code of the exec until it is reported as ended; -1 if it is not within exitPollTries tries
// or timeout, or if the daemon does not say.
func exitCode(ctx context.Context, d dockerapi.Docker, id string, every, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for i := 1; ; i++ {
		res, err := d.ExecInspect(ctx, id, client.ExecInspectOptions{})
		if err != nil {
			return -1
		}
		if !res.Running {
			return res.ExitCode
		}
		if i == exitPollTries {
			return -1
		}
		select {
		case <-ctx.Done():
			return -1
		case <-time.After(every):
		}
	}
}
