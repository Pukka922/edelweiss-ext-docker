package httpx

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v3"

	"github.com/Pukka922/edelweiss/sdk"
)

func TestBody(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	app.Post("/", func(c fiber.Ctx) error {
		var v struct{ Name string }
		if err := Body(c, &v); err != nil {
			return err
		}
		return c.SendString(v.Name)
	})
	ok, _ := app.Test(httptest.NewRequest("POST", "/", strings.NewReader(`{"Name":"web"}`)))
	if ok.StatusCode != 200 {
		t.Fatalf("valid body: %d", ok.StatusCode)
	}
	bad, _ := app.Test(httptest.NewRequest("POST", "/", strings.NewReader(`{`)))
	if bad.StatusCode != 400 {
		t.Fatalf("invalid body: %d", bad.StatusCode)
	}
}
