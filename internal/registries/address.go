package registries

import (
	"errors"
	"regexp"
	"strconv"
	"strings"
)

// DockerHub is the address of Docker Hub in the list.
const DockerHub = "docker.io"

// dockerHubServer is the server address Docker Hub logins carry, as docker login writes it.
const dockerHubServer = "https://index.docker.io/v1/"

var hostPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)*$`)

// NormalizeAddress turns what a user types into host[:port]: without scheme, path or trailing slash, in lower case;
// the names of Docker Hub without a port become DockerHub.
func NormalizeAddress(s string) (string, error) {
	a := strings.ToLower(strings.TrimSpace(s))
	a = strings.TrimPrefix(a, "https://")
	a = strings.TrimPrefix(a, "http://")
	if i := strings.IndexByte(a, '/'); i >= 0 {
		a = a[:i]
	}
	if a == "" {
		return "", errors.New("is required")
	}
	host, port := a, ""
	if i := strings.LastIndexByte(a, ':'); i >= 0 {
		host, port = a[:i], a[i+1:]
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 || strconv.Itoa(n) != port {
			return "", errors.New("has an invalid port")
		}
	}
	if !hostPattern.MatchString(host) {
		return "", errors.New("must be a host name or an IPv4 address, optionally with :port")
	}
	if port == "" {
		switch host {
		case "docker.io", "index.docker.io", "registry-1.docker.io":
			return DockerHub, nil
		}
		return host, nil
	}
	return host + ":" + port, nil
}

// HostOf returns the registry of an image reference: its first path part if that looks like a host (contains "." or
// ":", or is "localhost"), otherwise Docker Hub.
func HostOf(ref string) string {
	first, _, found := strings.Cut(ref, "/")
	if !found || !(strings.ContainsAny(first, ".:") || first == "localhost") {
		return DockerHub
	}
	if addr, err := NormalizeAddress(first); err == nil {
		return addr
	}
	return strings.ToLower(first)
}

// ServerAddress is what the daemon gets as the server of a login to address.
func ServerAddress(address string) string {
	if address == DockerHub {
		return dockerHubServer
	}
	return address
}
