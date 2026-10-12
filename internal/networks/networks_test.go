package networks

import (
	"context"
	"fmt"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/gofiber/fiber/v3"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/Pukka922/edelweiss-ext-docker/internal/dockerapi"
	"github.com/Pukka922/edelweiss-ext-docker/internal/dockertest"
	"github.com/Pukka922/edelweiss/sdk"
	"github.com/Pukka922/edelweiss/sdk/sdktest"
)

var anna = sdk.User{Name: "anna", UID: 1000}

func testApp(d dockerapi.Docker) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: sdk.ErrorHandler})
	Register(app, d)
	return app
}

func summary(name, driver, subnet string) network.Summary {
	n := network.Network{Name: name, ID: name + "-id", Driver: driver, Scope: "local"}
	if subnet != "" {
		n.IPAM.Config = []network.IPAMConfig{{Subnet: netip.MustParsePrefix(subnet), Gateway: netip.MustParsePrefix(subnet).Addr().Next()}}
	}
	return network.Summary{Network: n}
}

func TestList(t *testing.T) {
	f := &dockertest.Fake{
		NetworkListFn: func(context.Context, client.NetworkListOptions) (client.NetworkListResult, error) {
			return client.NetworkListResult{Items: []network.Summary{summary("front", "bridge", "172.30.0.0/24"), summary("bridge", "bridge", "172.17.0.0/16"), summary("host", "host", "")}}, nil
		},
		ContainerListFn: func(context.Context, client.ContainerListOptions) (client.ContainerListResult, error) {
			return client.ContainerListResult{Items: []container.Summary{{ID: "c1aaaaaaaaaaaaaaaa", Names: []string{"/web"},
				NetworkSettings: &container.NetworkSettingsSummary{Networks: map[string]*network.EndpointSettings{"front": {}}}}}}, nil
		},
	}
	got := sdktest.DecodeJSON[[]Network](t, sdktest.Do(t, testApp(f), sdktest.Request("GET", "/networks", nil, anna)))
	if len(got) != 3 || got[0].Name != "bridge" || !got[0].Predefined || got[1].Name != "front" || got[1].Subnet != "172.30.0.0/24" ||
		got[1].Gateway != "172.30.0.1" || got[1].Containers != 1 || got[2].Subnet != "" {
		t.Fatalf("networks %+v", got)
	}
}

func TestCreate(t *testing.T) {
	var name string
	var opts client.NetworkCreateOptions
	f := &dockertest.Fake{NetworkCreateFn: func(_ context.Context, n string, o client.NetworkCreateOptions) (client.NetworkCreateResult, error) {
		name, opts = n, o
		return client.NetworkCreateResult{ID: "new-id"}, nil
	}}
	body := `{"name":"lan","driver":"macvlan","subnet":"192.168.1.0/24","gateway":"192.168.1.1","options":{"parent":"eth0"}}`
	resp := sdktest.Do(t, testApp(f), sdktest.Request("POST", "/networks", strings.NewReader(body), anna))
	if resp.StatusCode != 201 || name != "lan" || opts.Driver != "macvlan" || opts.Options["parent"] != "eth0" ||
		opts.IPAM == nil || opts.IPAM.Config[0].Subnet.String() != "192.168.1.0/24" || opts.IPAM.Config[0].Gateway.String() != "192.168.1.1" {
		t.Fatalf("create: %d %s %+v", resp.StatusCode, name, opts)
	}
	for _, bad := range []string{
		`{"name":"x y"}`,
		`{"name":"net","driver":"overlay"}`,
		`{"name":"net","subnet":"10.0.0.1/8"}`,
		`{"name":"net","subnet":"bad"}`,
		`{"name":"net","gateway":"10.0.0.1"}`,
		`{"name":"net","subnet":"10.0.0.0/24","gateway":"10.0.1.1"}`,
	} {
		if resp := sdktest.Do(t, testApp(f), sdktest.Request("POST", "/networks", strings.NewReader(bad), anna)); resp.StatusCode != 400 {
			t.Errorf("%s: %d, want 400", bad, resp.StatusCode)
		}
	}
}

func TestConnectDisconnectDelete(t *testing.T) {
	var connect client.NetworkConnectOptions
	var disconnect client.NetworkDisconnectOptions
	removed := ""
	f := &dockertest.Fake{
		NetworkConnectFn: func(_ context.Context, _ string, o client.NetworkConnectOptions) (client.NetworkConnectResult, error) {
			connect = o
			return client.NetworkConnectResult{}, nil
		},
		NetworkDisconnectFn: func(_ context.Context, _ string, o client.NetworkDisconnectOptions) (client.NetworkDisconnectResult, error) {
			disconnect = o
			return client.NetworkDisconnectResult{}, nil
		},
		NetworkInspectFn: func(_ context.Context, id string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
			return client.NetworkInspectResult{Network: network.Inspect{Network: network.Network{ID: id, Name: id}}}, nil
		},
		NetworkRemoveFn: func(_ context.Context, id string, _ client.NetworkRemoveOptions) (client.NetworkRemoveResult, error) {
			removed = id
			return client.NetworkRemoveResult{}, nil
		},
	}
	app := testApp(f)
	if resp := sdktest.Do(t, app, sdktest.Request("POST", "/networks/front/connect", strings.NewReader(`{"container":"web","ipv4":"172.30.0.10"}`), anna)); resp.StatusCode != 204 ||
		connect.Container != "web" || connect.EndpointConfig.IPAMConfig.IPv4Address.String() != "172.30.0.10" {
		t.Fatalf("connect: %d %+v", resp.StatusCode, connect)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("POST", "/networks/front/connect", strings.NewReader(`{"container":"web","ipv4":"fe80::1"}`), anna)); resp.StatusCode != 400 {
		t.Fatalf("connect with an IPv6 address: %d", resp.StatusCode)
	}
	if resp := sdktest.Do(t, app, sdktest.Request("POST", "/networks/front/disconnect", strings.NewReader(`{"container":"web"}`), anna)); resp.StatusCode != 204 || disconnect.Container != "web" {
		t.Fatalf("disconnect: %d %+v", resp.StatusCode, disconnect)
	}
	for _, predefined := range []string{"bridge", "host", "none"} {
		if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/networks/"+predefined, nil, anna)); resp.StatusCode != 409 || errorCode(t, resp) != "network_predefined" {
			t.Fatalf("delete %s: %d", predefined, resp.StatusCode)
		}
	}
	if resp := sdktest.Do(t, app, sdktest.Request("DELETE", "/networks/front", nil, anna)); resp.StatusCode != 204 || removed != "front" {
		t.Fatalf("delete: %d %q", resp.StatusCode, removed)
	}
}

// errorCode is the code of an error answer ({"error":{"code":...}}).
func errorCode(t *testing.T, resp *http.Response) string {
	t.Helper()
	return sdktest.DecodeJSON[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, resp).Error.Code
}

func TestDeleteWithConnectedContainers(t *testing.T) {
	removeCalled := false
	f := &dockertest.Fake{
		NetworkInspectFn: func(_ context.Context, id string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
			return client.NetworkInspectResult{Network: network.Inspect{
				Network:    network.Network{ID: id + "-id", Name: id},
				Containers: map[string]network.EndpointResource{"c1aaaaaaaaaaaaaaaa": {Name: "web"}},
			}}, nil
		},
		NetworkRemoveFn: func(context.Context, string, client.NetworkRemoveOptions) (client.NetworkRemoveResult, error) {
			removeCalled = true
			return client.NetworkRemoveResult{}, nil
		},
	}
	resp := sdktest.Do(t, testApp(f), sdktest.Request("DELETE", "/networks/front", nil, anna))
	if resp.StatusCode != 409 || errorCode(t, resp) != "conflict" || removeCalled {
		t.Fatalf("delete with a connected container: %d, NetworkRemove called: %v", resp.StatusCode, removeCalled)
	}
}

func TestDeleteRaceWithConnect(t *testing.T) {
	// A container connects between the inspect and the removal: Docker 29 answers 403 for "active endpoints".
	remove := func(err error) *http.Response {
		f := &dockertest.Fake{
			NetworkInspectFn: func(_ context.Context, id string, _ client.NetworkInspectOptions) (client.NetworkInspectResult, error) {
				return client.NetworkInspectResult{Network: network.Inspect{Network: network.Network{ID: id + "-id", Name: id}}}, nil
			},
			NetworkRemoveFn: func(context.Context, string, client.NetworkRemoveOptions) (client.NetworkRemoveResult, error) {
				return client.NetworkRemoveResult{}, err
			},
		}
		return sdktest.Do(t, testApp(f), sdktest.Request("DELETE", "/networks/front", nil, anna))
	}
	busy := fmt.Errorf("Error response from daemon: error while removing network: network front has active endpoints: %w", cerrdefs.ErrPermissionDenied)
	resp := remove(busy)
	body := sdktest.DecodeJSON[struct {
		Error struct{ Code, Message string } `json:"error"`
	}](t, resp)
	if resp.StatusCode != 409 || body.Error.Code != "conflict" || !strings.HasPrefix(body.Error.Message, "error while removing network") {
		t.Fatalf("active endpoints: %d %+v, want 409 conflict with Docker's message", resp.StatusCode, body)
	}
	missing := fmt.Errorf("Error response from daemon: network front not found: %w", cerrdefs.ErrNotFound)
	if resp := remove(missing); resp.StatusCode != 404 || errorCode(t, resp) != "not_found" {
		t.Fatalf("other errors keep their mapping: %d", resp.StatusCode)
	}
}
