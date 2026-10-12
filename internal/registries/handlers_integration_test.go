//go:build integration

package registries

import (
	"strings"
	"testing"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

func TestLoginAgainstRegistries(t *testing.T) {
	cli := dockertest.Live(t)
	open := dockertest.Registry(t, cli, 5100, "", "")
	locked := dockertest.Registry(t, cli, 5101, "alice", "wonderland")
	s, _ := testStore(t)
	h := testApp(s, cli)
	try := func(body string) TestResult {
		t.Helper()
		return sdktest.DecodeJSON[TestResult](t, sdktest.Do(t, h, sdktest.Request("POST", "/registries/test", strings.NewReader(body), admin)))
	}
	if r := try(`{"address":"` + open + `"}`); !r.OK {
		t.Fatalf("registry without login: %+v", r)
	}
	if r := try(`{"address":"` + locked + `","auth":true,"username":"alice","password":"wonderland"}`); !r.OK {
		t.Fatalf("right password: %+v", r)
	}
	if r := try(`{"address":"` + locked + `","auth":true,"username":"alice","password":"wrong"}`); r.OK || !strings.Contains(r.Message, "401") {
		t.Fatalf("wrong password: %+v", r)
	}
	if r := try(`{"address":"` + locked + `"}`); r.OK {
		t.Fatalf("no login at a locked registry: %+v", r)
	}
}
