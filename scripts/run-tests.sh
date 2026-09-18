#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/modules.sh"

RUN_RUNTIME_SUITE=""
NO_INFRA=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --with-runtime) RUN_RUNTIME_SUITE=yes; shift ;;
    --no-infra) NO_INFRA=yes; shift ;;
    -h|--help)
      cat <<USAGE
Test runner for the Celerity Go SDK.

Usage:
  bash scripts/run-tests.sh [--with-runtime]

Options:
  -h, --help        Show this message
  --with-runtime    Also run the suite in tests/, bringing up the Celerity
                    runtime container it exercises the SDK against
  --no-infra        With --with-runtime, do not manage the container, for when
                    it is already running

The default run needs nothing installed. The IPC protocol is covered against a
stand-in runtime built from the same generated server stubs the contract
produces, served over a real unix socket, so the bytes on the wire are the bytes
on a real one.

--with-runtime adds what a stand-in cannot cover: routing, blueprint parsing and
tag construction are the runtime's, and the suite in tests/ is where the SDK's
idea of them is checked against the real thing. It needs the
celerity-runtime-core image, which is not released yet.
USAGE
      exit 0
      ;;
    *) shift ;;
  esac
done

cd "$ROOT"
mkdir -p coverage

# -count=1 so a cached pass is never mistaken for a run, -race because the
# dispatch loop is the part most worth catching a data race in.
GO_TEST_FLAGS=(-count=1 -race -timeout 120s -covermode=atomic)

for module in "${MODULES[@]}"; do
  profile="$ROOT/coverage/$(echo "$module" | tr '/.' '_').out"
  echo "testing $module"
  (cd "$ROOT/$module" && go test "${GO_TEST_FLAGS[@]}" \
    -coverprofile="$profile" -coverpkg=./... ./...)
done

# Merged across modules: a per-module report would show core's coverage of
# adapter code as a gap in both.
gocovmerge "$ROOT"/coverage/*.out > "$ROOT/coverage.txt" 2>/dev/null \
  || cat "$ROOT"/coverage/*.out > "$ROOT/coverage.txt"

if [ -n "$RUN_RUNTIME_SUITE" ]; then
  if [ -f "$ROOT/.env.test" ]; then
    set -a
    # shellcheck disable=SC1091
    source "$ROOT/.env.test"
    set +a
  fi

  if [ -z "$NO_INFRA" ]; then
    # The runtime launches the handlers executable itself, so it has to exist,
    # and it has to be built for the container's platform rather than the
    # developer's. The image is pulled first so its architecture can be read
    # rather than assumed.
    image="$(docker compose --env-file "$ROOT/.env.test" \
      -f "$ROOT/docker-compose.test-deps.yml" config --images runtime)"

    # Pulled on every run rather than only when absent, so a republished tag is
    # picked up instead of being served from the local cache.
    docker pull -q "$image" > /dev/null
    arch="$(docker image inspect "$image" --format '{{.Architecture}}')"

    echo "building the handlers executable for linux/$arch..."
    mkdir -p "$ROOT/tests/.build"
    (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
      go build -trimpath -o tests/.build/handlers ./tests/testapp)

    echo "starting the Celerity runtime..."
    docker compose --env-file "$ROOT/.env.test" \
      -f "$ROOT/docker-compose.test-deps.yml" up -d --wait

    stop_runtime() {
      echo "stopping the Celerity runtime..."
      docker compose --env-file "$ROOT/.env.test" \
        -f "$ROOT/docker-compose.test-deps.yml" down -v
    }
    trap stop_runtime EXIT
  fi

  echo "running the suite against a real runtime"
  go test "${GO_TEST_FLAGS[@]}" -tags integration ./tests/...
fi

if [ -z "${GITHUB_ACTIONS:-}" ]; then
  go tool cover -html="$ROOT/coverage.txt" -o "$ROOT/coverage.html"
  echo ""
  echo "coverage report: coverage.html"
fi

if [ -n "${GITHUB_ACTIONS:-}" ]; then
  go test -count=1 -json ./... > "$ROOT/report.json" || true
fi

echo ""
echo "tests complete"
