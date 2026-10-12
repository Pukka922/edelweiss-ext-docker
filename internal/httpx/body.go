// Package httpx holds small helpers for the extension's HTTP handlers.
package httpx

import (
	"encoding/json"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss/sdk"
)

// Body decodes the JSON request body into v.
func Body(c fiber.Ctx, v any) error {
	if err := json.Unmarshal(c.Body(), v); err != nil {
		return sdk.NewError(fiber.StatusBadRequest, "invalid_json", "request body must be valid JSON")
	}
	return nil
}
