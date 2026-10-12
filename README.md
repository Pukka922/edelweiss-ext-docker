# edelweiss-ext-docker

> **Note:** This project is vibe coded, for lack of time, and made only for my personal use: to manage my Raspberry Pi
> more comfortably. Anyone is welcome to use it, but at their own risk.

Docker management for [edelweiss](https://github.com/Pukka922/edelweiss), the web administration interface for Alpine
Linux, in the manner of a small Portainer. Its page **Docker** (menu section Containers) manages the local Docker
daemon:

- **Containers**: list with state and published ports; create and edit (image, command, ports, volumes or bind mounts,
  environment, network, restart policy, privileged); start, stop, restart, delete. Per container its logs (live),
  statistics (CPU, memory, network, disk) and a console: a shell in the container, in the browser.
- **Images**: which containers use them; pull from Docker Hub or a stored registry, with progress; delete; remove all
  unused images.
- **Volumes** and **networks**: create (with driver options, for example NFS volumes or macvlan networks), delete;
  connect containers to networks and disconnect them.
- **Registries** with or without login, for pulls; the connection can be tested.

Container names start with a letter or digit and have at least two characters (Docker's rule).

Editing a container changes its name and restart policy directly. Every other change recreates it: the old container is
replaced by a new one with the same name, and its named and anonymous volumes keep their data. Settings the form does
not show (memory limits, devices, capabilities, further networks and the like) are kept, and so is an explicit host IP
such as `0.0.0.0`. The options of bind mounts and `-v` volumes (for example `rslave`, SELinux `z`/`Z`, `nocopy`) and
host port ranges set outside edelweiss (an empty host port in the form keeps them) are kept while the mount (type,
source, target) or the port (container port, protocol, host IP) stays the same. The defaults of the image (its
environment, command, labels) are not copied, so a new image tag brings its own. If recreating fails, the old container
is restored.

Any edit, even of the name alone, recreates the container when its image tag now names a different image from the one
it runs (for example after a pull elsewhere). The option **Pull the image again before saving** pulls first; if the pull
brought a newer image, the container is recreated.

Leaving the console ends a shell that waits at its prompt. A program still running in it (a command, an editor) keeps
running until it ends, because Docker cannot stop a process started with `docker exec`.

It is one static binary with a Go backend (`main.go`, `internal/`) and a SolidJS interface (`ui/`), built with the
edelweiss SDKs.
[Developing edelweiss extensions](https://github.com/Pukka922/edelweiss/blob/main/docs/extension-development.md)
explains its parts.

## Requirements

Docker on the edelweiss host; as root:

```sh
apk add docker
rc-update add docker default
rc-service docker start
```

edelweiss refuses to install the extension while the package `docker` is missing, and names `apk add docker`.

Who may use Docker in edelweiss:

- members of the group `docker` (as root: `addgroup <user> docker`; it takes effect at their next sign-in to edelweiss).
  Membership of `docker` is as powerful as root: a container can mount the host's file system.
- everybody in admin mode; the backend then runs as root.

Without either, the page explains both ways.

- The registries, with their passwords, are stored in the SQLite database
  `/var/lib/edelweiss-ext-docker/registries.db`: owner root, group `docker`, mode 0640. Members of `docker` see the
  list and can test the connections; only admin mode changes it. The web interface never shows a stored password.
- Removing the extension deletes `/var/lib/edelweiss-ext-docker` with the registries; an update keeps it. In diskless
  mode add `lbu include /var/lib/edelweiss-ext-docker`.
- A registry without TLS (`http://`) also needs `"insecure-registries": ["<host>:<port>"]` in `/etc/docker/daemon.json`
  and `rc-service docker restart`. The extension does not change the daemon's configuration.
- On a Raspberry Pi the kernel may not account memory per container; the statistics then show no memory figures. Add
  `cgroup_enable=memory cgroup_memory=1` to `cmdline.txt` and reboot.

## Installation

On the edelweiss host, as root:

```sh
edelweissd ext install Pukka922/edelweiss-ext-docker
```

or in the web interface under **Settings → Extensions → Install from GitHub** (admin mode). edelweiss shows the
release, the file and its SHA-256 together with a warning, and installs only after you confirm. Updates:
`edelweissd ext update docker`, or the extensions page.

## Building

Requirements: Go 1.26, Node.js 22 with npm, make, a C compiler (`gcc`: `make test` runs the Go tests with the race
detector, which needs cgo), and Docker and `curl` for `make test-docker`.

```sh
make build                    # the UI, then static binaries for amd64 and arm64 in build/
make test                     # Go unit tests against a fake Docker (race detector), vitest, the release guards (scripts/)
make test-docker              # Go integration tests against a throwaway Docker daemon (see below)
make lint                     # go vet, gofmt, Biome, TypeScript, actionlint
make release VERSION=0.1.0    # release files in dist/: edelweiss-ext-docker_0.1.0_linux_<amd64|arm64>, checksums.txt
```

`make test-docker` (`scripts/test-docker.sh`) never uses the Docker daemon of your machine for the tests: it starts a
privileged `docker:29.9.0-dind` container whose API listens only on a random port of 127.0.0.1, runs the tests with
the build tag `integration` against it (`go test -p 1`) and removes the container afterwards, also when a test fails.
It needs network access: inside the throwaway daemon the tests pull `busybox:1.37` and `registry:3.1.2` from Docker
Hub, and they start test registries with and without login there.

The Go module requires `github.com/Pukka922/edelweiss/sdk`, and `ui/package.json` names the JS SDK by its download
URL at the edelweiss release. After the first publication both come from GitHub (the Go module proxy and
`npm ci`). Before that, build with `make dev-sdk` (next section).

## Development

Develop against an edelweiss checkout next to this repository (`../edelweiss`; `EDELWEISS_DIR=<path>` for another
place):

```sh
make dev-sdk
make build test lint
```

`make dev-sdk`:

- writes a `go.work` (git-ignored) whose `replace` points the Go SDK to `../edelweiss/sdk`;
- builds the JS SDK tarball there (`make sdk-pack`);
- installs it into `ui/node_modules` with `npm install --no-save --no-package-lock`, so `ui/package.json` stays
  unchanged and no `package-lock.json` is written.

None of this needs github.com.

- Do not run `go get`, `go mod tidy` or a plain `npm install` in `ui/` while the SDK version you require is
  unpublished: all three would look it up on GitHub. For a new Go requirement use
  `make tidy GET=<module>@<version>`: it runs `go get` and `go mod tidy` with a temporary `replace` to the local SDK and
  removes the `replace` again.
- To switch back to the published SDKs, run `rm -f go.work go.work.sum && rm -rf ui/node_modules`. The next `make`
  runs `npm ci`, but only after the first publication: before it there is no `ui/package-lock.json`, and `make` asks
  for `make dev-sdk` again.

To try the extension, start the edelweiss dev environment with its own Docker daemon (`make dev DOCKER=1` in
`../edelweiss`, then http://localhost:9090) and install the release file built here into its container (use
`linux_arm64` on an arm64 machine). `make dev` also builds the system overview, so it needs a checkout of
[edelweiss-ext-system](https://github.com/Pukka922/edelweiss-ext-system) at `../edelweiss-ext-system` (or
`EXT_SYSTEM_DIR=<path>` set) and stops with a message if there is none.

```sh
make release
mkdir -p ../edelweiss/build
cp dist/edelweiss-ext-docker_0.1.0_linux_amd64 ../edelweiss/build/
cd ../edelweiss
make dev DOCKER=1
docker compose -f dev/compose.yaml exec edelweiss /work/build/dev/edelweissd ext install \
  /work/build/edelweiss-ext-docker_0.1.0_linux_amd64 --config /work/dev/edelweiss.dev.toml
```

The running edelweissd picks the extension up within a moment. Install again after a change: that replaces the
extension and stops its processes.

- `DOCKER=1` adds the compose service `docker` (`docker:29.9.0-dind`), a Docker daemon of its own whose socket the dev
  container sees as `/var/run/docker.sock`; the Docker of your machine only runs it. Its images stay in a volume
  between runs. `docker compose -f dev/compose.yaml exec docker docker ps` talks to it directly.
- The test account `user` uses Docker through the group `docker` (registries read-only); `admin` sees "No access to
  Docker" until admin mode. The accounts are in `../edelweiss/dev/seed-users.sh`.
- Without `DOCKER=1` the installation fails with "missing packages: docker", which is the check of `requires`.
- `make dev` recreates the dev container and with it its installed extensions: install the file again afterwards.

Files have LF line endings (`.gitattributes`, `.editorconfig`), also in a Windows checkout.

## Releasing

1. Set the new version in `manifest.json` and commit. It has to be a plain `x.y.z` or `x.y.z-pre` by the rules of Go (no
   leading zeros, no empty pre-release identifier, no `+build` metadata).
2. Check it with `make release-check VERSION=<version>`.
3. Push `main`, then the tag: `git push origin main`, then `git tag v<version> && git push origin v<version>`.

The release workflow (`.github/workflows/release.yml`) has two jobs, so that the build cannot use the write token.
`build` has a read-only token and keeps no credentials in the checkout. It checks the tag name (`v` and then only
letters, digits, `.`, `+` and `-`), then runs three make targets and hands `dist/` on as a workflow artifact:

- `make release-check`: the tag must match the manifest's version, a plain `x.y.z` or `x.y.z-pre`;
- `make lint test`: the checks and unit tests of the CI workflow, so a tag on a commit that fails them publishes
  nothing (the integration tests run in the CI workflow; tag a commit whose CI run passed);
- `make release`: the release files in `dist/`.

`publish` runs when `build` has succeeded. It holds the write token (gh needs it), builds nothing, needs neither Go nor
Node and keeps no credentials in its checkout either; only the `gh-release` step gets the token, through `GH_TOKEN`. It
downloads the artifact into `dist/` and runs `make gh-release`: it checks that `dist/` holds exactly `checksums.txt` and
the two binaries of this version, that the files match `checksums.txt` and that it lists exactly them, and creates the
GitHub release `v<version>` with `edelweiss-ext-docker_<version>_linux_amd64`,
`edelweiss-ext-docker_<version>_linux_arm64` and `checksums.txt`. A version with a `-` (`0.2.0-rc.1`) is published as a
GitHub pre-release: the installer takes the latest release, which leaves pre-releases out, so an rc is not offered as an
update.

A compromised build could still hand over altered files under the expected names: `checksums.txt` shows that the files
are consistent, not where they come from.

**Published versions are immutable.** Once a release may have been fetched, never move or delete the tag `v<version>`
and never replace or delete the files of the release:

- edelweiss shows users the SHA-256 of the file and refuses a release that changed after they looked at it
  (`release_changed`);
- sum.golang.org records every version of this Go module that has been fetched, so a moved tag makes `go get` fail with
  "checksum mismatch" for everyone, new fetchers included, not only for those who fetched the old one.

A broken release is fixed with a new patch version.

Rerunning a failed workflow is fine as long as the tag stays on the same commit. "Re-run failed jobs" repeats only the
job that failed, and "Re-run all jobs" builds again (the upload of the artifact replaces the one of the first attempt).
`gh release create` fails if the release already exists, so if the workflow had already created the GitHub release,
delete only the release (never the tag) before the rerun. Do that only for a release that nobody can have downloaded
yet; otherwise publish a new patch version.

The CI workflow (`.github/workflows/ci.yml`) runs `make lint test test-docker build` on every push and pull request.

## First publication

Until edelweiss publishes its SDKs, this repository has no `ui/package-lock.json`, and `go.sum` has no entry for
`github.com/Pukka922/edelweiss/sdk`. Its workflows fail until then. The order:

1. Publish edelweiss `v0.1.0` (its README, "Releasing"). Its release workflow attaches `edelweiss-sdk-0.1.0.tgz` and
   sets the tag `sdk/v0.1.0`.
2. Here, switch to the published SDKs and commit the lock data:

   ```sh
   rm -f go.work go.work.sum
   rm -rf ui/node_modules
   go get github.com/Pukka922/edelweiss/sdk@v0.1.0
   go mod tidy
   (cd ui && npm install)
   make lint test test-docker build
   git add go.mod go.sum ui/package-lock.json
   git commit -m "build: use the published edelweiss sdks v0.1.0"
   ```

   If `go get` reports an unknown revision right after the edelweiss release, the module proxy has not seen the tag
   yet. Wait a few minutes, or run `GOPROXY=direct go get github.com/Pukka922/edelweiss/sdk@v0.1.0`. From then on
   `go get` and `go mod tidy` work directly, and `make tidy` is no longer needed.
3. Create the public repository `Pukka922/edelweiss-ext-docker` on GitHub. Recommended before the first tag: a tag
   ruleset (Settings → Rules → Rulesets, target: tags) that blocks deleting and updating the tags `v*`, so that a
   published version cannot be moved by mistake (see Releasing above). Then push `main` and the tag:

   ```sh
   git remote add origin https://github.com/Pukka922/edelweiss-ext-docker.git
   git push -u origin main
   git tag v0.1.0
   git push origin v0.1.0
   ```

   When the release workflow has finished, `edelweissd ext install Pukka922/edelweiss-ext-docker` installs the
   extension.

## License

MIT, see LICENSE.
