//go:build integration

package networks

import (
	"strings"
	"testing"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

func TestNetworksAgainstDaemon(t *testing.T) {
	cli := dockertest.Live(t)
	app := testApp(cli)
	name := dockertest.Name(t, "net")
	resp := sdktest.Do(t, app, sdktest.Request("POST", "/networks", strings.NewReader(`{"name":"`+name+`","subnet":"172.31.250.0/24","gateway":"172.31.250.1"}`), anna))
	if resp.StatusCode != 201 {
		t.Fatalf("create: %d", resp.StatusCode)
	}
	id := sdktest.DecodeJSON[struct {
		ID string `json:"id"`
	}](t, resp).ID
	dockertest.RemoveNetworkAtEnd(t, cli, id)
	web := dockertest.Name(t, "web")
	dockertest.Run(t, cli, client.ContainerCreateOptions{Name: web, Config: &container.Config{Image: dockertest.Busybox, Cmd: []string{"sleep", "300"}}})

	if resp := sdktest.Do(t, app, sdktest.Request("POST", "/networks/"+id+"/connect", strings.NewReader(`{"container":"`+web+`","ipv4":"172.31.250.10"}`), anna)); resp.StatusCode != 204 {
		t.Fatalf("connect: %d", resp.StatusCode)
	}
	detail := sdktest.DecodeJSON[Detail](t, sdktest.Do(t, app, sdktest.Request("GET", "/networks/"+id, nil, anna)))
	if len(detail.Attached) != 1 || detail.Attached[0].Name != web || detail.Attached[0].IPv4 != "172.31.250.10" || detail.Subnet != "172.31.250.0/24" {
		t.Fatalf("detail %+v", detail)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/networks/"+id, nil, anna)); resp.StatusCode != 409 || errorCode(t, resp) != "conflict" {
		t.Fatalf("delete with a connected container: %d", resp.StatusCode)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("POST", "/networks/"+id+"/disconnect", strings.NewReader(`{"container":"`+web+`"}`), anna)); resp.StatusCode != 204 {
		t.Fatalf("disconnect: %d", resp.StatusCode)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/networks/bridge", nil, anna)); resp.StatusCode != 409 {
		t.Fatalf("delete bridge: %d", resp.StatusCode)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/networks/"+id, nil, anna)); resp.StatusCode != 204 {
		t.Fatalf("delete: %d", resp.StatusCode)
	}
}
