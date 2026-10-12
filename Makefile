# edelweiss-ext-docker: Docker management for edelweiss (https://github.com/Pukka922/edelweiss): containers, images,
# volumes, networks and registries of the local Docker daemon.
GO ?= go
# The edelweiss checkout that make dev-sdk takes the SDKs from.
EDELWEISS_DIR ?= ../edelweiss
EDELWEISS_ABS = $(abspath $(EDELWEISS_DIR))
BINARY := edelweiss-ext-docker
ARCHES := amd64 arm64
GO_PKGS := . ./internal/...
SDK_MODULE := github.com/Pukka922/edelweiss/sdk
ACTIONLINT := github.com/rhysd/actionlint/cmd/actionlint@v1.7.12

MANIFEST_VERSION := $(shell sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' manifest.json)
ifeq ($(MANIFEST_VERSION),)
$(error cannot read the version from manifest.json)
endif
# The version of a release (the tag without "v"); make release-check compares it with the manifest.
VERSION ?= $(MANIFEST_VERSION)
# The files of a release in dist/, besides checksums.txt: make release writes them, make gh-release uploads exactly these.
RELEASE_FILES = $(foreach arch,$(ARCHES),$(BINARY)_$(VERSION)_linux_$(arch))

# npm's record of the installed packages: npm ci writes it, and so does make dev-sdk.
UI_DEPS := ui/node_modules/.package-lock.json

.PHONY: build ui test test-docker lint lint-workflows release-check release gh-release dev-sdk tidy

# The UI (embedded by main.go), then static binaries for amd64 and arm64 in build/.
build: ui
	mkdir -p build
	for arch in $(ARCHES); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch $(GO) build -trimpath -ldflags "-s -w" \
			-o build/$(BINARY)_linux_$$arch . || exit 1; \
	done

ui: $(UI_DEPS)
	cd ui && npm run build

test: $(UI_DEPS)
	$(GO) test -race $(GO_PKGS)
	cd ui && npm test
	sh scripts/release-guards-test.sh

# Integration tests (build tag integration) against a throwaway Docker daemon in a container (scripts/test-docker.sh),
# never against the Docker of this machine. Needs docker and network access (the tests pull busybox and registry:3.1.2).
test-docker:
	GO=$(GO) sh scripts/test-docker.sh $(GO_PKGS)

lint:$(UI_DEPS) lint-workflows
	$(GO) vet $(GO_PKGS)
	@test -z "$$(gofmt -l main.go internal)" || (gofmt -l main.go internal; echo "gofmt needed"; exit 1)
	cd ui && npx biome check .
	cd ui && npm run typecheck

lint-workflows:
	GOWORK=off $(GO) run $(ACTIONLINT) -shellcheck= -pyflakes=

# The first check is the format of VERSION, by Go's rules for a version without the v: x.y.z or x.y.z-pre, numbers without
# leading zeros, no empty pre-release identifier and no leading zero in a numeric one, and no +build metadata (Go
# ignores it in a version tag). The value reaches the check through the environment, so the shell does not parse it as
# code, and the line after it only runs for a plain version. That protects the shell lines only: make itself still
# expands $(...) in a VERSION from the command line, so the tag-name check of the release workflow is the real boundary
# there.
# One pre-release identifier: digits without a leading zero, or characters of 0-9A-Za-z- with a letter or a hyphen.
SEMVER_ID := (0|[1-9][0-9]*|[0-9A-Za-z-]*[A-Za-z-][0-9A-Za-z-]*)
SEMVER_RE := ^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-$(SEMVER_ID)(\.$(SEMVER_ID))*)?$$
release-check: export RELEASE_CHECK_VERSION := $(VERSION)
release-check:
	@export LC_ALL=C; bad=; \
	case "$$RELEASE_CHECK_VERSION" in \
		""|*[!0-9A-Za-z.-]*) bad=1 ;; \
		*) printf '%s\n' "$$RELEASE_CHECK_VERSION" | grep -Eq '$(SEMVER_RE)' || bad=1 ;; \
	esac; \
	test -z "$$bad" || { \
		printf 'release-check: VERSION=%s is not a version like 0.1.0 or 0.2.0-rc.1 (no v prefix, no +build metadata, no leading zeros)\n' \
			"$$RELEASE_CHECK_VERSION" >&2; exit 1; }
	@test "$(VERSION)" = "$(MANIFEST_VERSION)" || { \
		echo "release-check: version $(VERSION) does not match manifest.json ($(MANIFEST_VERSION)); the tag must be v$(MANIFEST_VERSION)" >&2; \
		exit 1; }
	@echo "release-check: $(VERSION) OK"

# The release files in dist/ (the release workflow uploads all of them).
release: release-check build
	rm -rf dist
	mkdir -p dist
	for arch in $(ARCHES); do cp build/$(BINARY)_linux_$$arch dist/$(BINARY)_$(VERSION)_linux_$$arch || exit 1; done
	cd dist && sha256sum $(RELEASE_FILES) > checksums.txt

# Release workflow only (the publish job, which builds nothing): the GitHub release v<version> with the files of
# make release (needs gh and GH_TOKEN). dist/ has to hold exactly checksums.txt and RELEASE_FILES: the files match
# checksums.txt, which lists exactly them, and nothing else is there (no stale file of another version, no hidden file).
# A version with a pre-release part (a "-", like 0.2.0-rc.1) is published as a pre-release: the installer takes GitHub's
# latest release, which leaves pre-releases out, so an rc is not offered as an update.
# gh release create fails if the release already exists, so a rerun needs the release (or draft) deleted first, never
# the tag.
gh-release: release-check
	@test -f dist/checksums.txt || { echo "gh-release: dist/checksums.txt is missing; run make release first" >&2; exit 1; }
	cd dist && sha256sum -c checksums.txt
	@test "$$(ls -A dist | LC_ALL=C sort)" = "$$(printf '%s\n' checksums.txt $(RELEASE_FILES) | LC_ALL=C sort)" || { \
		echo "gh-release: dist/ has to hold exactly checksums.txt and $(RELEASE_FILES); run make release first" >&2; exit 1; }
	@test "$$(sed 's/^[0-9a-f]\{64\} [ *]//' dist/checksums.txt | LC_ALL=C sort)" = "$$(printf '%s\n' $(RELEASE_FILES) | LC_ALL=C sort)" || { \
		echo "gh-release: dist/checksums.txt has to list exactly $(RELEASE_FILES); run make release first" >&2; exit 1; }
	gh release create "v$(VERSION)" --verify-tag --title "$(BINARY) $(VERSION)" --generate-notes \
		$(if $(findstring -,$(VERSION)),--prerelease) $(addprefix dist/,checksums.txt $(RELEASE_FILES))

ifneq ($(wildcard ui/package-lock.json),)
$(UI_DEPS): ui/package-lock.json ui/package.json
	cd ui && npm ci
else
$(UI_DEPS):
	@echo "ui/node_modules is missing, and there is no ui/package-lock.json yet (the edelweiss SDKs are not published):" >&2
	@echo "run make dev-sdk first (README.md, Development)" >&2
	@exit 1
endif

# Develop against the edelweiss checkout in EDELWEISS_DIR instead of the published SDKs: a go.work (not checked in)
# whose replace points the Go SDK to that checkout, and that checkout's JS SDK tarball in ui/node_modules
# (ui/package.json stays unchanged, no package-lock.json is written). Needs no access to github.com.
dev-sdk:
	@test -f "$(EDELWEISS_ABS)/sdk/go.mod" -a -f "$(EDELWEISS_ABS)/web/sdk/package.json" || { \
		echo "dev-sdk: no edelweiss checkout at $(EDELWEISS_ABS) (EDELWEISS_DIR=$(EDELWEISS_DIR)):" >&2; \
		echo "clone github.com/Pukka922/edelweiss next to this repository or set EDELWEISS_DIR=<path>" >&2; \
		exit 1; }
	rm -f go.work go.work.sum
	$(GO) work init .
	$(GO) work edit -replace $(SDK_MODULE)=$(EDELWEISS_ABS)/sdk
	$(MAKE) -C $(EDELWEISS_ABS) sdk-pack
	v=$$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' $(EDELWEISS_ABS)/web/sdk/package.json) && \
		cd ui && npm install --no-save --no-package-lock --no-audit --no-fund $(EDELWEISS_ABS)/dist/edelweiss-sdk-$$v.tgz

# Go requirements before the first publication of edelweiss: go get and go mod tidy cannot fetch the unpublished SDK, so
# they run with a temporary replace to the SDK in EDELWEISS_DIR, which is removed again (go.sum then has no entry for the
# SDK, as before). GET names modules to add at exact versions, e.g. make tidy GET=github.com/moby/moby/client@v0.6.2.
# make itself defines GET (= get, for SCCS), so only a GET from the command line or the environment counts.
tidy:
	$(GO) mod edit -replace $(SDK_MODULE)=$(EDELWEISS_ABS)/sdk
	GOWORK=off sh -c '$(if $(filter command line environment,$(origin GET)),$(GO) get $(GET) && )$(GO) mod tidy'; s=$$?; \
		$(GO) mod edit -dropreplace $(SDK_MODULE); exit $$s
