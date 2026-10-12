//go:build integration

package console

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	fws "github.com/fasthttp/websocket"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
)

// sleeper starts a container that only sleeps, and returns the daemon's client and its ID.
func sleeper(t *testing.T) (*client.Client, string) {
	t.Helper()
	cli := dockertest.Live(t)
	id := dockertest.Run(t, cli, client.ContainerCreateOptions{Name: dockertest.Name(t, "console"),
		Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sleep", "300"}}})
	return cli, id
}

// readUntil reads output until it contains want.
func readUntil(t *testing.T, c *fws.Conn, want string) {
	t.Helper()
	var seen strings.Builder
	for !strings.Contains(seen.String(), want) {
		mt, out, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("output so far %q, waiting for %q: %v", seen.String(), want, err)
		}
		if mt == fws.BinaryMessage {
			seen.Write(out)
		}
	}
}

// exitCodeOf reads until the exit frame and returns its code.
func exitCodeOf(t *testing.T, c *fws.Conn) int {
	t.Helper()
	for {
		mt, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("no exit frame: %v", err)
		}
		if mt == fws.TextMessage {
			var exit struct {
				Type string `json:"type"`
				Code int    `json:"code"`
			}
			if json.Unmarshal(msg, &exit) != nil || exit.Type != "exit" {
				t.Fatalf("exit frame %q", msg)
			}
			return exit.Code
		}
	}
}

func TestConsoleAgainstDaemon(t *testing.T) {
	cli, id := sleeper(t)
	c := dial(t, serve(t, cli)+"/containers/"+id+"/console?cmd=/bin/sh")
	_ = c.WriteMessage(fws.BinaryMessage, []byte("echo edelweiss-$((40+2))\n"))
	readUntil(t, c, "edelweiss-42\r\n")
	_ = c.WriteMessage(fws.BinaryMessage, []byte("exit\n"))
	if code := exitCodeOf(t, c); code != 0 {
		t.Fatalf("exit code %d", code)
	}
}

func TestConsoleReportsTheExitCode(t *testing.T) {
	cli, id := sleeper(t)
	c := dial(t, serve(t, cli)+"/containers/"+id+"/console")
	_ = c.WriteMessage(fws.BinaryMessage, []byte("exit 7\n"))
	if code := exitCodeOf(t, c); code != 7 {
		t.Fatalf("exit code %d, want 7", code)
	}
	if _, _, err := c.ReadMessage(); !fws.IsCloseError(err, fws.CloseNormalClosure) {
		t.Fatalf("close: %v", err)
	}
}

// shells counts the processes of the container whose command is exactly sh.
func shells(t *testing.T, cli *client.Client, id string) int {
	t.Helper()
	top, err := cli.ContainerTop(context.Background(), id, client.ContainerTopOptions{})
	if err != nil {
		t.Fatal(err)
	}
	col := slices.IndexFunc(top.Titles, func(title string) bool { return title == "COMMAND" || title == "CMD" })
	if col < 0 {
		t.Fatalf("no command column in %q", top.Titles)
	}
	n := 0
	for _, p := range top.Processes {
		if col < len(p) && p[col] == "sh" {
			n++
		}
	}
	return n
}

// A shell of the console does not outlive the browser: stdin of the exec is closed, so the shell ends instead of
// lingering in the container.
func TestConsoleEndsTheShellWhenTheBrowserLeaves(t *testing.T) {
	cli, id := sleeper(t)
	c := dial(t, serve(t, cli)+"/containers/"+id+"/console?cmd=sh")
	_ = c.WriteMessage(fws.BinaryMessage, []byte("echo up-$((40+2))\n"))
	readUntil(t, c, "up-42\r\n")
	if n := shells(t, cli, id); n != 1 {
		t.Fatalf("%d shells in the container while the console is open, want 1", n)
	}
	c.Close()
	deadline := time.Now().Add(5 * time.Second)
	for shells(t, cli, id) != 0 {
		if time.Now().After(deadline) {
			t.Fatal("the shell of the console is still running 5 s after the browser left")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
