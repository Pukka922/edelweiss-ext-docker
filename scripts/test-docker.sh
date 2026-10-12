#!/bin/sh
# make test-docker: runs the integration tests (go test -tags integration) against a throwaway Docker daemon
# (docker:29.9.0-dind, privileged), never against the Docker of this machine. The daemon listens on a random port of
# 127.0.0.1 only and is removed afterwards, also when the tests fail. Packages run one at a time (-p 1): they share the
# daemon, and some tests (image prune) look at all of its images.
set -eu
IMAGE=${DIND_IMAGE:-docker:29.9.0-dind}
NAME=edelweiss-ext-docker-test-$$
# --tls=false: dockerd 29 slows its start by 15 s on a TCP listener without TLS unless told explicitly
docker run -d --rm --privileged --name "$NAME" -e DOCKER_TLS_CERTDIR= -p 127.0.0.1::2375 "$IMAGE" --tls=false >/dev/null
trap 'docker rm -f -v "$NAME" >/dev/null 2>&1 || true' EXIT INT TERM
PORT=$(docker port "$NAME" 2375/tcp | sed -n 's/^127\.0\.0\.1:\([0-9][0-9]*\)$/\1/p' | head -n 1)
[ -n "$PORT" ] || { echo "test-docker: no port published for $NAME" >&2; exit 1; }
i=0
until curl -fsS "http://127.0.0.1:$PORT/_ping" >/dev/null 2>&1; do
  i=$((i + 1))
  if [ "$i" -ge 60 ]; then
    echo "test-docker: the daemon in $NAME did not answer within 30 s" >&2
    docker logs "$NAME" >&2 || true
    exit 1
  fi
  sleep 0.5
done
EDELWEISS_DOCKER_TEST_HOST="tcp://127.0.0.1:$PORT" "${GO:-go}" test -tags integration -count=1 -p 1 "$@"
