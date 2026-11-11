package registries

import (
	"strings"
	"testing"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

// Since Fiber 3.6 / fasthttp 1.75 the request path is cleaned (RFC 3986 dot-segment removal) before routing, so a "." or
// ".." segment in the id position no longer reaches a handler as an id. The ids go to the registry store, not to Docker;
// the test pins the statuses that the cleaned paths give today.
func TestDotSegmentIdsAreOtherRoutes(t *testing.T) {
	s, _ := testStore(t)
	app := testApp(s, &dockertest.Fake{})
	do := func(method, id string) int {
		return sdktest.Do(t, app, sdktest.Request(method, "/registries/"+id, strings.NewReader("{}"), admin)).StatusCode
	}
	singleDots := []string{".", "%2e"}
	doubleDots := []string{"..", "%2e%2e", "%2E%2E", ".%2e", "%2e."}
	for _, method := range []string{"PUT", "DELETE"} {
		for _, id := range singleDots { // the cleaned path is /registries, which has no PUT or DELETE
			if got := do(method, id); got != 405 {
				t.Errorf("%s /registries/%s: %d, want 405", method, id, got)
			}
		}
		for _, id := range doubleDots {
			if got := do(method, id); got != 404 {
				t.Errorf("%s /registries/%s: %d, want 404", method, id, got)
			}
		}
		// Positive control: an ordinary id reaches the handler (the store answers for an unknown one, not 405).
		if got := do(method, "nope"); got == 405 {
			t.Errorf("%s /registries/nope: %d, the route is not reached", method, got)
		}
	}
}
