package registries

import (
	"errors"
	"strings"

	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/httpx"
	"github.com/Pukka922/edelweiss/sdk"
)

// View is an entry as the UI sees it: never with its password.
type View struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Address     string `json:"address"`
	Username    string `json:"username,omitempty"`
	HasPassword bool   `json:"hasPassword"`
}

func viewOf(r Registry) View {
	return View{ID: r.ID, Name: r.Name, Address: r.Address, Username: r.Username, HasPassword: r.Password != ""}
}

// TestRequest asks for a login test: of a stored entry (ID alone), or of form values (admin mode only, they belong to
// editing; with ID, an empty password means the stored one).
type TestRequest struct {
	ID       string `json:"id"`
	Address  string `json:"address"`
	Auth     bool   `json:"auth"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// TestResult is the daemon's answer to a login test.
type TestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

type handlers struct {
	store  *Store
	docker dockerapi.Docker
}

// Register adds the registry routes.
func Register(r fiber.Router, s *Store, d dockerapi.Docker) {
	h := &handlers{store: s, docker: d}
	r.Get("/registries", h.list)
	r.Post("/registries/test", h.test)
	r.Post("/registries", sdk.RequireAdmin(), h.add)
	r.Put("/registries/:id", sdk.RequireAdmin(), h.update)
	r.Delete("/registries/:id", sdk.RequireAdmin(), h.remove)
}

func (h *handlers) list(c fiber.Ctx) error {
	list, err := h.store.List()
	if err != nil {
		return Error(err)
	}
	views := make([]View, 0, len(list))
	for _, r := range list {
		views = append(views, viewOf(r))
	}
	return c.JSON(fiber.Map{"registries": views, "writable": sdk.UserFrom(c).Admin})
}

func (h *handlers) add(c fiber.Ctx) error {
	var in Input
	if err := httpx.Body(c, &in); err != nil {
		return err
	}
	r, err := h.store.Add(in)
	if err != nil {
		return Error(err)
	}
	return c.Status(fiber.StatusCreated).JSON(viewOf(r))
}

func (h *handlers) update(c fiber.Ctx) error {
	var in Input
	if err := httpx.Body(c, &in); err != nil {
		return err
	}
	r, err := h.store.Update(c.Params("id"), in)
	if err != nil {
		return Error(err)
	}
	return c.JSON(viewOf(r))
}

func (h *handlers) remove(c fiber.Ctx) error {
	if err := h.store.Delete(c.Params("id")); err != nil {
		return Error(err)
	}
	return c.SendStatus(fiber.StatusNoContent)
}

func (h *handlers) test(c fiber.Ctx) error {
	var req TestRequest
	if err := httpx.Body(c, &req); err != nil {
		return err
	}
	var login client.RegistryLoginOptions
	if req.Address == "" && req.ID != "" {
		r, err := h.store.Find(req.ID)
		if err != nil {
			return Error(err)
		}
		login = client.RegistryLoginOptions{ServerAddress: ServerAddress(r.Address), Username: r.Username, Password: r.Password}
	} else {
		if !sdk.UserFrom(c).Admin {
			return sdk.NewError(fiber.StatusForbidden, "admin_required", "administrative access required")
		}
		addr, err := NormalizeAddress(req.Address)
		if err != nil {
			return Error(&FieldError{"address", err.Error()})
		}
		login.ServerAddress = ServerAddress(addr)
		if req.Auth {
			login.Username, login.Password = strings.TrimSpace(req.Username), req.Password
			if login.Password == "" && req.ID != "" {
				if r, err := h.store.Find(req.ID); err == nil {
					login.Password = r.Password
				}
			}
		}
	}
	_, err := h.docker.RegistryLogin(c.Context(), login)
	if a := dockerapi.AccessOf(err); a == dockerapi.AccessUnavailable || a == dockerapi.AccessForbidden {
		return dockerapi.Error(err)
	}
	if err != nil {
		// the daemon answers a refused login with HTTP 500 and the registry's reason
		return c.JSON(TestResult{OK: false, Message: dockerapi.Message(err)})
	}
	return c.JSON(TestResult{OK: true})
}

// Error turns an error of the store into the extension's answer.
func Error(err error) error {
	var fe *FieldError
	switch {
	case errors.As(err, &fe):
		return sdk.NewError(fiber.StatusBadRequest, "invalid_registry", fe.Error()).WithDetails(map[string]any{"field": fe.Field})
	case errors.Is(err, ErrNotFound):
		return sdk.NewError(fiber.StatusNotFound, "not_found", err.Error())
	case errors.Is(err, ErrExists):
		return sdk.NewError(fiber.StatusConflict, "registry_exists", err.Error())
	case errors.Is(err, ErrForbidden):
		return sdk.NewError(fiber.StatusForbidden, "registries_forbidden", err.Error())
	case errors.Is(err, ErrCorrupt):
		return sdk.NewError(fiber.StatusInternalServerError, "registries_corrupt", err.Error())
	default:
		return err
	}
}
