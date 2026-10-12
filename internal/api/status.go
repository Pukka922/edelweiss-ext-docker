package api

import (
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss/sdk"
)

// Status is the answer of GET /status: whether the extension can reach Docker, and the daemon's version.
type Status struct {
	Docker     dockerapi.Access `json:"docker"`
	Version    string           `json:"version,omitempty"`
	APIVersion string           `json:"apiVersion,omitempty"`
	OS         string           `json:"os,omitempty"`
	Arch       string           `json:"arch,omitempty"`
	// CPUs and Memory (bytes) of the Docker host, for the limits of the container form; empty if Docker does not say.
	CPUs   int   `json:"cpus,omitempty"`
	Memory int64 `json:"memory,omitempty"`
	Admin  bool  `json:"admin"`
	// Message is Docker's text when the daemon is unavailable or refused the user.
	Message string `json:"message,omitempty"`
}

func status(d dockerapi.Docker) fiber.Handler {
	return func(c fiber.Ctx) error {
		st := Status{Admin: sdk.UserFrom(c).Admin}
		v, err := d.ServerVersion(c.Context(), client.ServerVersionOptions{})
		switch access := dockerapi.AccessOf(err); access {
		case dockerapi.AccessOK:
			st.Docker, st.Version, st.APIVersion, st.OS, st.Arch = access, v.Version, v.APIVersion, v.Os, v.Arch
			if info, err := d.Info(c.Context(), client.InfoOptions{}); err == nil {
				st.CPUs, st.Memory = info.Info.NCPU, info.Info.MemTotal
			}
		case dockerapi.AccessUnavailable, dockerapi.AccessForbidden:
			st.Docker, st.Message = access, err.Error()
		default:
			return dockerapi.Error(err)
		}
		return c.JSON(st)
	}
}
