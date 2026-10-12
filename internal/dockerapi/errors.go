package dockerapi

import (
	"errors"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss/sdk"
)

// Access says whether the extension can talk to the daemon.
type Access string

const (
	AccessOK          Access = "ok"
	AccessUnavailable Access = "unavailable" // no socket, nobody listening, not reachable
	AccessForbidden   Access = "forbidden"   // the socket refused this user (not in the group docker)
)

// AccessOf classifies err: AccessOK for nil, AccessForbidden or AccessUnavailable for a failed connection to the
// daemon, "" for any other error (an answer of the daemon, a cancelled request).
func AccessOf(err error) Access {
	switch {
	case err == nil:
		return AccessOK
	case !client.IsErrConnectionFailed(err):
		return ""
	// the client keeps no errno for EACCES, only this text (research notes, section 6)
	case strings.Contains(err.Error(), "permission denied"):
		return AccessForbidden
	default:
		return AccessUnavailable
	}
}

// Message is Docker's text of err without the prefix the client adds to answers of the daemon.
func Message(err error) string {
	return strings.TrimPrefix(err.Error(), "Error response from daemon: ")
}

// InvalidID is the answer for a container :id that SafeID refuses: 400 invalid_request with details.field "id". Every
// route with a container :id gives it before it talks to Docker.
func InvalidID() error {
	return sdk.NewError(fiber.StatusBadRequest, "invalid_request", "id must be the name or the ID of a container").
		WithDetails(map[string]any{"field": "id"})
}

// Error turns the error of a Docker call into the extension's answer. An *sdk.APIError passes through unchanged.
func Error(err error) error {
	if err == nil {
		return nil
	}
	var apiErr *sdk.APIError
	if errors.As(err, &apiErr) {
		return err
	}
	msg := Message(err)
	switch {
	case AccessOf(err) == AccessForbidden:
		return sdk.NewError(fiber.StatusForbidden, "docker_forbidden", msg)
	case AccessOf(err) == AccessUnavailable:
		return sdk.NewError(fiber.StatusServiceUnavailable, "docker_unavailable", msg)
	case cerrdefs.IsNotFound(err):
		return sdk.NewError(fiber.StatusNotFound, "not_found", msg)
	case cerrdefs.IsConflict(err):
		return sdk.NewError(fiber.StatusConflict, "conflict", msg)
	case cerrdefs.IsInvalidArgument(err):
		return sdk.NewError(fiber.StatusBadRequest, "invalid_request", msg)
	default:
		return sdk.NewError(fiber.StatusBadGateway, "docker_error", msg)
	}
}
