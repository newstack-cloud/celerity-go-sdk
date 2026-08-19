#!/usr/bin/env bash
set -euo pipefail

# Regenerates the Go stubs from the vendored contract.
#
# The stubs are committed rather than produced at build time, following the
# runtime's reasoning about its own Rust stubs: a build that needs a code
# generator needs it in every matrix leg of every release workflow, and a leg
# that misses it fails at release rather than in CI.
#
# Changing proto/ without running this leaves the two out of step. CI checks for
# exactly that, so it is caught rather than merged.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

OUT_DIR="${1:-$ROOT/internal/ipcproto/celerityv1}"
GO_PACKAGE="github.com/newstack-cloud/celerity-go-sdk/internal/ipcproto/celerityv1"
PROTO="celerity/runtime/v1/runtime.proto"

for tool in protoc protoc-gen-go protoc-gen-go-grpc; do
  if ! command -v "$tool" > /dev/null; then
    cat <<MISSING
$tool is not on PATH.

  brew install protobuf
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
  export PATH=\$PATH:\$(go env GOPATH)/bin
MISSING
    exit 1
  fi
done

staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

protoc -I "$ROOT/proto" \
  --go_out="$staging" --go_opt=paths=source_relative \
  --go_opt="M$PROTO=$GO_PACKAGE" \
  --go-grpc_out="$staging" --go-grpc_opt=paths=source_relative \
  --go-grpc_opt="M$PROTO=$GO_PACKAGE" \
  "$PROTO"

mkdir -p "$OUT_DIR"
rm -f "$OUT_DIR"/*.pb.go
mv "$staging/celerity/runtime/v1"/*.go "$OUT_DIR/"

echo "generated stubs in $OUT_DIR"
