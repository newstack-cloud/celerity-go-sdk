#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/modules.sh"

RUN_INTEGRATION=""
NO_INFRA=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --with-integration) RUN_INTEGRATION=yes; shift ;;
    --no-infra) NO_INFRA=yes; shift ;;
    -h|--help)
      cat <<USAGE
Test runner for the Celerity Go SDK.

Usage:
  bash scripts/run-tests.sh [--with-integration]

Options:
  -h, --help           Show this message
  --with-integration   Also run every integration suite, bringing up what they
                       read including the Celerity runtime, Valkey and service emulators
  --no-infra           To be used with the --with-integration flag,
                       for when dependencies are already running

The default run does not need anything installed. The IPC protocol is covered against a
stand-in runtime built from the same generated server stubs the contract
produces, served over a real unix socket, so the bytes on the wire are the bytes
on a real one.

--with-integration Runs tests that integrate with services such as AWS along
                   with an integrated suite with the real Celerity runtime.
USAGE
      exit 0
      ;;
    *) shift ;;
  esac
done

cd "$ROOT"

# Cleared rather than added to as a profile left by an earlier run covers code
# this one did not exercise, as otherwise, a run without --with-integration would report
# the integration coverage of the run before it.
mkdir -p coverage
rm -f "$ROOT"/coverage/*.out "$ROOT/report.json"

# -count=1 so a cached pass is never mistaken for a run, -race because the
# dispatch loop is the part most worth catching a data race in.
GO_TEST_FLAGS=(-count=1 -race -timeout 120s -covermode=atomic)

# go_test runs one suite, and in CI also records it in the report Sonar reads.
#
# Recorded from the runs themselves rather than from a pass of their own, so
# that the report covers what the coverage covers. A separate pass would have to
# repeat every run to see the same tests, and an integration suite repeated is
# an integration suite run twice.
#
# The log carries the JSON in CI, which is what makes both possible from one
# run. pipefail is set, so a failing suite still fails the script.
go_test() {
  if [ -n "${GITHUB_ACTIONS:-}" ]; then
    go test -json "$@" | tee -a "$ROOT/report.json"
  else
    go test "$@"
  fi
}

# profile_for names a module's coverage file, with a suffix separating the runs
# that cover the same code from different sides.
profile_for() {
  echo "$ROOT/coverage/$(echo "$1" | tr '/.' '_')${2:+_$2}.out"
}

# production_packages lists what coverage is measured against, which is the SDK
# and not what exercises it.
#
# tests/ holds the suite that drives a real runtime and tests/testapp the
# application it drives, neither of which is code this SDK ships. Counted, the
# fixture reads as production code not covered, because the process that runs
# it is not the process being measured, and it takes the reported figure down
# with it.
production_packages() {
  go list ./... | grep -v "/tests" | paste -sd, -
}

for module in "${MODULES[@]}"; do
  echo "testing $module"
  (cd "$ROOT/$module" && go_test "${GO_TEST_FLAGS[@]}" \
    -coverprofile="$(profile_for "$module")" -coverpkg="$(production_packages)" ./...)
done

if [ -n "$RUN_INTEGRATION" ]; then
  if [ -f "$ROOT/.env.test" ]; then
    set -a
    # shellcheck disable=SC1091
    source "$ROOT/.env.test"
    set +a
  fi

  compose() {
    docker compose --env-file "$ROOT/.env.test" \
      -f "$ROOT/docker-compose.test-deps.yml" "$@"
  }

  if [ -z "$NO_INFRA" ]; then
    # The runtime launches the handlers executable itself, so it has to exist,
    # and it has to be built for the container's platform rather than the
    # developer's. The image is pulled first so its architecture can be read
    # rather than assumed.
    #
    # Named rather than taken from `config --images runtime`, which returns the
    # image of every service the runtime depends on as well as its own, in an
    # order that is not stable between calls. The version still comes from
    # compose and .env.test, so there is one definition of it.
    image="$(compose config --images | grep celerity-runtime-core)"

    if [ "$(printf '%s' "$image" | grep -c .)" -ne 1 ]; then
      echo "expected exactly one runtime image, resolved: $image" >&2
      exit 1
    fi

    # Pulled on every run rather than only when absent, so a republished tag is
    # picked up instead of being served from the local cache.
    docker pull -q "$image" > /dev/null
    arch="$(docker image inspect "$image" --format '{{.Architecture}}')"

    echo "building the handlers executable for linux/$arch..."
    mkdir -p "$ROOT/tests/.build"
    (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
      go build -trimpath -o tests/.build/handlers ./tests/testapp)

    echo "starting the services the integration suites read..."
    compose up -d --wait

    stop_infra() {
      echo "stopping the services..."
      compose down -v
    }
    trap stop_infra EXIT
  fi

  echo "running the suite against a real runtime"
  go_test "${GO_TEST_FLAGS[@]}" -tags integration \
    -coverprofile="$(profile_for runtime-suite)" \
    -coverpkg="$(production_packages)" ./tests/...

  # A provider module's own integration suite, against the services it calls
  # rather than a stand-in of them.
  #
  # Counted towards coverage like any other run. These cover the paths a
  # stand-in cannot reach, the client an SDK builds for itself among them, so
  # leaving them out reports the best-covered code as the least.
  for module in "${MODULES[@]}"; do
    # The root module's own integration suite is the one against the runtime,
    # already run above. Looking for it here would find it a second time, and
    # would find the nested modules too, since they sit inside the root's tree.
    [ "$module" = "." ] && continue

    if grep -rq "go:build integration" --include='*_test.go' "$ROOT/$module"; then
      echo "running the integration suite for $module"
      (cd "$ROOT/$module" && go_test "${GO_TEST_FLAGS[@]}" -tags integration \
        -coverprofile="$(profile_for "$module" integration)" \
        -coverpkg="$(production_packages)" ./...)
    fi
  done
fi

# Merged after every run, so that a run adding coverage is counted rather than
# arriving after the report was written. Across modules too as a per-module report
# would show core's coverage of adapter code as a gap in both.
gocovmerge "$ROOT"/coverage/*.out > "$ROOT/coverage.txt" 2>/dev/null \
  || cat "$ROOT"/coverage/*.out > "$ROOT/coverage.txt"

if [ -z "${GITHUB_ACTIONS:-}" ]; then
  go tool cover -html="$ROOT/coverage.txt" -o "$ROOT/coverage.html"
  echo ""
  echo "coverage report: coverage.html"
fi

echo ""
echo "tests complete"
