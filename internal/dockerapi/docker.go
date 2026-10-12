// Package dockerapi is the extension's view of the Docker Engine API: the calls it makes (Docker, implemented by
// *client.Client of github.com/moby/moby/client) and how their errors become answers of the extension.
package dockerapi

import (
	"context"
	"regexp"
	"slices"
	"strings"

	"github.com/moby/moby/client"
)

// DefaultHost is the socket of the local Docker daemon. The extension does not read DOCKER_HOST: edelweissd starts
// the backend with a fixed environment, and a daemon on another host is out of scope.
const DefaultHost = "unix:///var/run/docker.sock"

// Docker is the part of the Docker client the extension uses. *client.Client implements it; unit tests use
// dockertest.Fake.
type Docker interface {
	Ping(ctx context.Context, options client.PingOptions) (client.PingResult, error)
	ServerVersion(ctx context.Context, options client.ServerVersionOptions) (client.ServerVersionResult, error)

	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerInspect(ctx context.Context, id string, options client.ContainerInspectOptions) (client.ContainerInspectResult, error)
	ContainerCreate(ctx context.Context, options client.ContainerCreateOptions) (client.ContainerCreateResult, error)
	ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStop(ctx context.Context, id string, options client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerRestart(ctx context.Context, id string, options client.ContainerRestartOptions) (client.ContainerRestartResult, error)
	ContainerRemove(ctx context.Context, id string, options client.ContainerRemoveOptions) (client.ContainerRemoveResult, error)
	ContainerRename(ctx context.Context, id string, options client.ContainerRenameOptions) (client.ContainerRenameResult, error)
	ContainerUpdate(ctx context.Context, id string, options client.ContainerUpdateOptions) (client.ContainerUpdateResult, error)
	ContainerLogs(ctx context.Context, id string, options client.ContainerLogsOptions) (client.ContainerLogsResult, error)
	ContainerStats(ctx context.Context, id string, options client.ContainerStatsOptions) (client.ContainerStatsResult, error)

	ExecCreate(ctx context.Context, containerID string, options client.ExecCreateOptions) (client.ExecCreateResult, error)
	ExecAttach(ctx context.Context, execID string, options client.ExecAttachOptions) (client.ExecAttachResult, error)
	ExecResize(ctx context.Context, execID string, options client.ExecResizeOptions) (client.ExecResizeResult, error)
	ExecInspect(ctx context.Context, execID string, options client.ExecInspectOptions) (client.ExecInspectResult, error)

	ImageList(ctx context.Context, options client.ImageListOptions) (client.ImageListResult, error)
	ImageInspect(ctx context.Context, image string, options ...client.ImageInspectOption) (client.ImageInspectResult, error)
	ImageRemove(ctx context.Context, image string, options client.ImageRemoveOptions) (client.ImageRemoveResult, error)
	ImagePrune(ctx context.Context, options client.ImagePruneOptions) (client.ImagePruneResult, error)
	ImagePull(ctx context.Context, ref string, options client.ImagePullOptions) (client.ImagePullResponse, error)

	VolumeList(ctx context.Context, options client.VolumeListOptions) (client.VolumeListResult, error)
	VolumeCreate(ctx context.Context, options client.VolumeCreateOptions) (client.VolumeCreateResult, error)
	VolumeRemove(ctx context.Context, name string, options client.VolumeRemoveOptions) (client.VolumeRemoveResult, error)

	NetworkList(ctx context.Context, options client.NetworkListOptions) (client.NetworkListResult, error)
	NetworkInspect(ctx context.Context, id string, options client.NetworkInspectOptions) (client.NetworkInspectResult, error)
	NetworkCreate(ctx context.Context, name string, options client.NetworkCreateOptions) (client.NetworkCreateResult, error)
	NetworkRemove(ctx context.Context, id string, options client.NetworkRemoveOptions) (client.NetworkRemoveResult, error)
	NetworkConnect(ctx context.Context, id string, options client.NetworkConnectOptions) (client.NetworkConnectResult, error)
	NetworkDisconnect(ctx context.Context, id string, options client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error)

	RegistryLogin(ctx context.Context, options client.RegistryLoginOptions) (client.RegistryLoginResult, error)
	Events(ctx context.Context, options client.EventsListOptions) client.EventsResult
}

var _ Docker = (*client.Client)(nil)

// New returns a client of the daemon at host (DefaultHost on a real system). The API version is negotiated on the first
// call. No timeout is set: logs, statistics and events stream for as long as the browser listens.
func New(host string) (*client.Client, error) {
	return client.New(client.WithHost(host), client.WithUserAgent("edelweiss-ext-docker"))
}

// NamePattern is Docker's rule for the names of containers and volumes.
var NamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]+$`)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*$`)

// SafeID reports whether id can be the :id of a container route: a name, an ID or a short ID. It must start with a
// letter or digit, so that "." and ".." never reach the daemon's URL path, where the client's path.Join would clean
// them into another endpoint (GET /containers/. would become GET /containers/json); Fiber routes on the raw path, so
// such an id does reach a handler.
func SafeID(id string) bool { return idPattern.MatchString(id) }

// SafeRef reports whether an image reference can go into the URL path of the daemon request unchanged: the Docker client
// builds the path with path.Join, which would clean a leading slash or a "." or ".." segment into another endpoint, and
// "?" or "#" would start a query or a fragment.
func SafeRef(ref string) bool {
	segments := strings.Split(ref, "/")
	return !strings.HasPrefix(ref, "/") && !strings.ContainsAny(ref, "?#") && !slices.Contains(segments, "..") && !slices.Contains(segments, ".")
}

var anonymousName = regexp.MustCompile(`^[0-9a-f]{64}$`)

// IsAnonymousVolume reports whether a volume is anonymous: Docker labels anonymous volumes with
// com.docker.volume.anonymous and names them with 64 hex characters (labels may be nil, e.g. for mount points).
func IsAnonymousVolume(name string, labels map[string]string) bool {
	if _, ok := labels["com.docker.volume.anonymous"]; ok {
		return true
	}
	return anonymousName.MatchString(name)
}
