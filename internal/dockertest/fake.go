// Package dockertest has test doubles of the Docker daemon: Fake for unit tests, and (live.go) helpers for the
// integration tests that run against the throwaway daemon of make test-docker.
package dockertest

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/moby/api/types/events"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
)

// Fake is a dockerapi.Docker whose methods call the function set for them. A method without a function returns an
// error, which handlers answer with 502 docker_error, so an unexpected call shows in the test.
type Fake struct {
	PingFn              func(context.Context, client.PingOptions) (client.PingResult, error)
	ServerVersionFn     func(context.Context, client.ServerVersionOptions) (client.ServerVersionResult, error)
	InfoFn              func(context.Context, client.InfoOptions) (client.SystemInfoResult, error)
	ContainerListFn     func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspectFn  func(context.Context, string, client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerCreateFn   func(context.Context, client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerStartFn    func(context.Context, string, client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStopFn     func(context.Context, string, client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerRestartFn  func(context.Context, string, client.ContainerRestartOptions) (client.ContainerRestartResult, error)
	ContainerRemoveFn   func(context.Context, string, client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ContainerRenameFn   func(context.Context, string, client.ContainerRenameOptions) (client.ContainerRenameResult, error)
	ContainerUpdateFn   func(context.Context, string, client.ContainerUpdateOptions) (client.ContainerUpdateResult, error)
	ContainerLogsFn     func(context.Context, string, client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	ContainerStatsFn    func(context.Context, string, client.ContainerStatsOptions) (client.ContainerStatsResult, error)
	ExecCreateFn        func(context.Context, string, client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttachFn        func(context.Context, string, client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecResizeFn        func(context.Context, string, client.ExecResizeOptions) (client.ExecResizeResult, error)
	ExecInspectFn       func(context.Context, string, client.ExecInspectOptions) (client.ExecInspectResult, error)
	ImageListFn         func(context.Context, client.ImageListOptions) (client.ImageListResult, error)
	ImageInspectFn      func(context.Context, string, ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImageRemoveFn       func(context.Context, string, client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	ImagePruneFn        func(context.Context, client.ImagePruneOptions) (client.ImagePruneResult, error)
	ImagePullFn         func(context.Context, string, client.ImagePullOptions) (client.ImagePullResponse, error)
	VolumeListFn        func(context.Context, client.VolumeListOptions) (client.VolumeListResult, error)
	VolumeCreateFn      func(context.Context, client.VolumeCreateOptions) (client.VolumeCreateResult, error)
	VolumeRemoveFn      func(context.Context, string, client.VolumeRemoveOptions) (client.VolumeRemoveResult, error)
	NetworkListFn       func(context.Context, client.NetworkListOptions) (client.NetworkListResult, error)
	NetworkInspectFn    func(context.Context, string, client.NetworkInspectOptions) (client.NetworkInspectResult, error)
	NetworkCreateFn     func(context.Context, string, client.NetworkCreateOptions) (client.NetworkCreateResult, error)
	NetworkRemoveFn     func(context.Context, string, client.NetworkRemoveOptions) (client.NetworkRemoveResult, error)
	NetworkConnectFn    func(context.Context, string, client.NetworkConnectOptions) (client.NetworkConnectResult, error)
	NetworkDisconnectFn func(context.Context, string, client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error)
	RegistryLoginFn     func(context.Context, client.RegistryLoginOptions) (client.RegistryLoginResult, error)
	EventsFn            func(context.Context, client.EventsListOptions) client.EventsResult
}

var _ dockerapi.Docker = (*Fake)(nil)

func notFaked(method string) error { return fmt.Errorf("dockertest: %s is not faked", method) }

func (f *Fake) Ping(ctx context.Context, o client.PingOptions) (client.PingResult, error) {
	if f.PingFn == nil {
		return client.PingResult{}, notFaked("Ping")
	}
	return f.PingFn(ctx, o)
}

func (f *Fake) ServerVersion(ctx context.Context, o client.ServerVersionOptions) (client.ServerVersionResult, error) {
	if f.ServerVersionFn == nil {
		return client.ServerVersionResult{}, notFaked("ServerVersion")
	}
	return f.ServerVersionFn(ctx, o)
}

func (f *Fake) Info(ctx context.Context, o client.InfoOptions) (client.SystemInfoResult, error) {
	if f.InfoFn == nil {
		return client.SystemInfoResult{}, notFaked("Info")
	}
	return f.InfoFn(ctx, o)
}

func (f *Fake) ContainerList(ctx context.Context, o client.ContainerListOptions) (client.ContainerListResult, error) {
	if f.ContainerListFn == nil {
		return client.ContainerListResult{}, notFaked("ContainerList")
	}
	return f.ContainerListFn(ctx, o)
}

func (f *Fake) ContainerInspect(ctx context.Context, id string, o client.ContainerInspectOptions) (client.ContainerInspectResult, error) {
	if f.ContainerInspectFn == nil {
		return client.ContainerInspectResult{}, notFaked("ContainerInspect")
	}
	return f.ContainerInspectFn(ctx, id, o)
}

func (f *Fake) ContainerCreate(ctx context.Context, o client.ContainerCreateOptions) (client.ContainerCreateResult, error) {
	if f.ContainerCreateFn == nil {
		return client.ContainerCreateResult{}, notFaked("ContainerCreate")
	}
	return f.ContainerCreateFn(ctx, o)
}

func (f *Fake) ContainerStart(ctx context.Context, id string, o client.ContainerStartOptions) (client.ContainerStartResult, error) {
	if f.ContainerStartFn == nil {
		return client.ContainerStartResult{}, notFaked("ContainerStart")
	}
	return f.ContainerStartFn(ctx, id, o)
}

func (f *Fake) ContainerStop(ctx context.Context, id string, o client.ContainerStopOptions) (client.ContainerStopResult, error) {
	if f.ContainerStopFn == nil {
		return client.ContainerStopResult{}, notFaked("ContainerStop")
	}
	return f.ContainerStopFn(ctx, id, o)
}

func (f *Fake) ContainerRestart(ctx context.Context, id string, o client.ContainerRestartOptions) (client.ContainerRestartResult, error) {
	if f.ContainerRestartFn == nil {
		return client.ContainerRestartResult{}, notFaked("ContainerRestart")
	}
	return f.ContainerRestartFn(ctx, id, o)
}

func (f *Fake) ContainerRemove(ctx context.Context, id string, o client.ContainerRemoveOptions) (client.ContainerRemoveResult, error) {
	if f.ContainerRemoveFn == nil {
		return client.ContainerRemoveResult{}, notFaked("ContainerRemove")
	}
	return f.ContainerRemoveFn(ctx, id, o)
}

func (f *Fake) ContainerRename(ctx context.Context, id string, o client.ContainerRenameOptions) (client.ContainerRenameResult, error) {
	if f.ContainerRenameFn == nil {
		return client.ContainerRenameResult{}, notFaked("ContainerRename")
	}
	return f.ContainerRenameFn(ctx, id, o)
}

func (f *Fake) ContainerUpdate(ctx context.Context, id string, o client.ContainerUpdateOptions) (client.ContainerUpdateResult, error) {
	if f.ContainerUpdateFn == nil {
		return client.ContainerUpdateResult{}, notFaked("ContainerUpdate")
	}
	return f.ContainerUpdateFn(ctx, id, o)
}

func (f *Fake) ContainerLogs(ctx context.Context, id string, o client.ContainerLogsOptions) (client.ContainerLogsResult, error) {
	if f.ContainerLogsFn == nil {
		return nil, notFaked("ContainerLogs")
	}
	return f.ContainerLogsFn(ctx, id, o)
}

func (f *Fake) ContainerStats(ctx context.Context, id string, o client.ContainerStatsOptions) (client.ContainerStatsResult, error) {
	if f.ContainerStatsFn == nil {
		return client.ContainerStatsResult{}, notFaked("ContainerStats")
	}
	return f.ContainerStatsFn(ctx, id, o)
}

func (f *Fake) ExecCreate(ctx context.Context, containerID string, o client.ExecCreateOptions) (client.ExecCreateResult, error) {
	if f.ExecCreateFn == nil {
		return client.ExecCreateResult{}, notFaked("ExecCreate")
	}
	return f.ExecCreateFn(ctx, containerID, o)
}

func (f *Fake) ExecAttach(ctx context.Context, execID string, o client.ExecAttachOptions) (client.ExecAttachResult, error) {
	if f.ExecAttachFn == nil {
		return client.ExecAttachResult{}, notFaked("ExecAttach")
	}
	return f.ExecAttachFn(ctx, execID, o)
}

func (f *Fake) ExecResize(ctx context.Context, execID string, o client.ExecResizeOptions) (client.ExecResizeResult, error) {
	if f.ExecResizeFn == nil {
		return client.ExecResizeResult{}, notFaked("ExecResize")
	}
	return f.ExecResizeFn(ctx, execID, o)
}

func (f *Fake) ExecInspect(ctx context.Context, execID string, o client.ExecInspectOptions) (client.ExecInspectResult, error) {
	if f.ExecInspectFn == nil {
		return client.ExecInspectResult{}, notFaked("ExecInspect")
	}
	return f.ExecInspectFn(ctx, execID, o)
}

func (f *Fake) ImageList(ctx context.Context, o client.ImageListOptions) (client.ImageListResult, error) {
	if f.ImageListFn == nil {
		return client.ImageListResult{}, notFaked("ImageList")
	}
	return f.ImageListFn(ctx, o)
}

func (f *Fake) ImageInspect(ctx context.Context, image string, o ...client.ImageInspectOption) (client.ImageInspectResult, error) {
	if f.ImageInspectFn == nil {
		return client.ImageInspectResult{}, notFaked("ImageInspect")
	}
	return f.ImageInspectFn(ctx, image, o...)
}

func (f *Fake) ImageRemove(ctx context.Context, image string, o client.ImageRemoveOptions) (client.ImageRemoveResult, error) {
	if f.ImageRemoveFn == nil {
		return client.ImageRemoveResult{}, notFaked("ImageRemove")
	}
	return f.ImageRemoveFn(ctx, image, o)
}

func (f *Fake) ImagePrune(ctx context.Context, o client.ImagePruneOptions) (client.ImagePruneResult, error) {
	if f.ImagePruneFn == nil {
		return client.ImagePruneResult{}, notFaked("ImagePrune")
	}
	return f.ImagePruneFn(ctx, o)
}

func (f *Fake) ImagePull(ctx context.Context, ref string, o client.ImagePullOptions) (client.ImagePullResponse, error) {
	if f.ImagePullFn == nil {
		return nil, notFaked("ImagePull")
	}
	return f.ImagePullFn(ctx, ref, o)
}

func (f *Fake) VolumeList(ctx context.Context, o client.VolumeListOptions) (client.VolumeListResult, error) {
	if f.VolumeListFn == nil {
		return client.VolumeListResult{}, notFaked("VolumeList")
	}
	return f.VolumeListFn(ctx, o)
}

func (f *Fake) VolumeCreate(ctx context.Context, o client.VolumeCreateOptions) (client.VolumeCreateResult, error) {
	if f.VolumeCreateFn == nil {
		return client.VolumeCreateResult{}, notFaked("VolumeCreate")
	}
	return f.VolumeCreateFn(ctx, o)
}

func (f *Fake) VolumeRemove(ctx context.Context, name string, o client.VolumeRemoveOptions) (client.VolumeRemoveResult, error) {
	if f.VolumeRemoveFn == nil {
		return client.VolumeRemoveResult{}, notFaked("VolumeRemove")
	}
	return f.VolumeRemoveFn(ctx, name, o)
}

func (f *Fake) NetworkList(ctx context.Context, o client.NetworkListOptions) (client.NetworkListResult, error) {
	if f.NetworkListFn == nil {
		return client.NetworkListResult{}, notFaked("NetworkList")
	}
	return f.NetworkListFn(ctx, o)
}

func (f *Fake) NetworkInspect(ctx context.Context, id string, o client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
	if f.NetworkInspectFn == nil {
		return client.NetworkInspectResult{}, notFaked("NetworkInspect")
	}
	return f.NetworkInspectFn(ctx, id, o)
}

func (f *Fake) NetworkCreate(ctx context.Context, name string, o client.NetworkCreateOptions) (client.NetworkCreateResult, error) {
	if f.NetworkCreateFn == nil {
		return client.NetworkCreateResult{}, notFaked("NetworkCreate")
	}
	return f.NetworkCreateFn(ctx, name, o)
}

func (f *Fake) NetworkRemove(ctx context.Context, id string, o client.NetworkRemoveOptions) (client.NetworkRemoveResult, error) {
	if f.NetworkRemoveFn == nil {
		return client.NetworkRemoveResult{}, notFaked("NetworkRemove")
	}
	return f.NetworkRemoveFn(ctx, id, o)
}

func (f *Fake) NetworkConnect(ctx context.Context, id string, o client.NetworkConnectOptions) (client.NetworkConnectResult, error) {
	if f.NetworkConnectFn == nil {
		return client.NetworkConnectResult{}, notFaked("NetworkConnect")
	}
	return f.NetworkConnectFn(ctx, id, o)
}

func (f *Fake) NetworkDisconnect(ctx context.Context, id string, o client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error) {
	if f.NetworkDisconnectFn == nil {
		return client.NetworkDisconnectResult{}, notFaked("NetworkDisconnect")
	}
	return f.NetworkDisconnectFn(ctx, id, o)
}

func (f *Fake) RegistryLogin(ctx context.Context, o client.RegistryLoginOptions) (client.RegistryLoginResult, error) {
	if f.RegistryLoginFn == nil {
		return client.RegistryLoginResult{}, notFaked("RegistryLogin")
	}
	return f.RegistryLoginFn(ctx, o)
}

// Events without a function returns a result whose Err channel delivers the "not faked" error at once.
func (f *Fake) Events(ctx context.Context, o client.EventsListOptions) client.EventsResult {
	if f.EventsFn == nil {
		errs := make(chan error, 1)
		errs <- notFaked("Events")
		return client.EventsResult{Messages: make(chan events.Message), Err: errs}
	}
	return f.EventsFn(ctx, o)
}

// Unavailable returns the error the Docker client reports when there is no daemon: a connection to a socket that does
// not exist.
func Unavailable(t testing.TB) error {
	t.Helper()
	cli, err := dockerapi.New("unix://" + filepath.Join(t.TempDir(), "missing.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	_, err = cli.Ping(context.Background(), client.PingOptions{})
	if !client.IsErrConnectionFailed(err) {
		t.Fatalf("expected a connection error, got %v", err)
	}
	return err
}

// Forbidden returns the error of a socket this user may not open (mode 000). Root ignores the mode, so the test is
// skipped as root.
func Forbidden(t testing.TB) error {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root may open any socket")
	}
	path := filepath.Join(t.TempDir(), "docker.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	cli, err := dockerapi.New("unix://" + path)
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	_, err = cli.Ping(context.Background(), client.PingOptions{})
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected permission denied, got %v", err)
	}
	return err
}
