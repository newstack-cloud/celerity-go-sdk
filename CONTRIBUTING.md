# Contributing

## Setup

```bash
git config core.hooksPath .githooks
npm install          # commitlint, used by the commit-msg hook
go work sync
bash scripts/run-tests.sh
```

`go.work` is committed. It is normally left out of a library, but every module
here is developed as a unit, so committing it means a clone builds without reconstruction of the workspace.

## Commands

| Command | What it does |
|---|---|
| `bash scripts/build.sh` | Build every module |
| `bash scripts/run-tests.sh` | Every test; needs nothing installed |
| `bash scripts/run-tests.sh --with-integration` | Also every integration suite, bringing up the runtime container and the services the provider modules read |
| `bash scripts/lint.sh` | `go vet` and staticcheck over every module |
| `bash scripts/check-format.sh` | Fail on anything not gofmt-formatted |
| `bash scripts/gen-proto.sh` | Regenerate the protocol stubs |
| `bash scripts/check-proto.sh` | Fail when the stubs and the contract disagree |
| `bash scripts/vulncheck.sh` | `govulncheck` over every module |

Standard library advisories are fixed by a Go patch release rather than by a
change here, so the workflows install the newest patch of the pinned minor
(`check-latest` on `actions/setup-go`) and the scan blocks on anything it still
reports. If it flags the standard library locally, your toolchain is behind
CI's.

## The protocol contract

`proto/celerity/runtime/v1/runtime.proto` is vendored from the Celerity
monorepo, which owns it. The generated stubs in `internal/ipcproto` are
committed rather than produced at build time, for the same reason the runtime
commits its Rust ones: a build that needs a code generator needs it in every
matrix leg of every release workflow, and a leg that misses it fails at release
rather than in CI.

Changing the vendored `.proto` without regenerating leaves the two out of step,
so do both in the same commit and review the generated diff: it is the clearest
signal of whether the change was additive or breaking. CI checks both, and
checks the vendored copy against the runtime's on every run.

Regenerating needs only buf:

```bash
brew install bufbuild/buf/buf
bash scripts/gen-proto.sh
```

The plugins are remote and pinned in `proto/buf.gen.yaml`, so there is nothing
else to install and a fresh generation produces the same bytes on any machine.
buf is also what lints the contract and checks it for breaking changes, which is
the same tool the runtime repository uses on the same file, so the two cannot
disagree about what the contract says.

`scripts/check-proto.sh` runs three checks:

1. `buf lint`, so a malformed contract is caught here.
2. The vendored contract against the runtime's. The Celerity monorepo is
   public, so buf reads the upstream contract straight out of it. It is the authoritative check where byte-identical is stricter
   than compatible, and it is the right requirement, because this repository is
   not where the contract is decided. When they differ, the difference is
   classified as breaking or compatible, since that is what decides whether a
   sync is routine.
3. A regeneration, diffed against the committed stubs.

Set `CELERITY_RUNTIME_PROTO` to a local checkout of the monorepo's
`libs/runtime/proto` to skip the fetch, which is faster and works offline.

## Commits

Conventional commits, enforced by commitlint through `.githooks/commit-msg`.

```
feat(core): register websocket handlers by route key
fix(ipc): grant credit when a handler panics
docs(repo): describe the two-pass extraction
```

**Types**: `feat`, `fix`, `build`, `revert`, `wip`, `chore`, `ci`, `docs`,
`style`, `refactor`, `perf`, `test`, `instr`, `deps`

**Scopes**: one per release unit, so a scope names the module a commit releases
and nothing finer.

| Scope | Module | Import path |
|---|---|---|
| `core` | `.` | `github.com/newstack-cloud/celerity-go-sdk` |
| `serverless-aws` | `serverless/aws` | `.../serverless/aws` |
| `resources-aws` | `resources/aws` | `.../resources/aws` |
| `config-aws` | `config/aws` | `.../config/aws` |
| `config-local` | `config/local` | `.../config/local` |
| `cli` | `cmd/celerity-go` | `.../cmd/celerity-go` |
| `deps` | — | dependency updates belonging to no module |

A scope naming a package rather than a module would read as though it decided
which module released, which it does not, and would drift as packages move
between modules.

The scope is optional. A repository-wide change that belongs to no module is
better without one: `chore: ignore the built binary` rather than an invented
scope for it.

## Releases

release-please opens a release PR per module. Merging it tags the module; Go
derives a submodule's version from a tag carrying its directory prefix, so
`serverless/aws` releases as `serverless/aws/v0.2.0` and core as `v0.2.0`.

**A commit belongs to a module by the files it touches, not by its scope.**
Editing `serverless/aws/adapter.go` releases `serverless/aws` whatever the
commit says; `feat(serverless-aws): ...` on a commit that only edits core
releases core. The scope drives the changelog and the PR title and nothing else,
so it is worth keeping it honest, but it is not what routes the release.

Two consequences of core being the repository root:

- Core would otherwise pick up every change in the repository, including the
  submodules'. `exclude-paths` in `release-please-config.json` keeps their
  directories out of it, so a `resources/aws` fix does not also bump core.
- Everything else at the root, the workflows, scripts, README and this file, is
  still core's. Use a type that does not release for those, `ci`, `docs`,
  `chore`, `refactor`, `style` or `test`, and reserve `feat` and `fix` for
  commits that change what core does. A `docs:` commit touching only the README
  produces no release; a `fix:` commit touching only the README produces one
  that changes no code.

Modules are versioned independently: a `resources/aws` fix should not bump core.
CI builds each module with `GOWORK=off`, so a workspace replace cannot mask a
`require` that a consumer would fail to resolve. For the same reason, when the
build tool's `require` of core moves, core is released first.

Library modules need no publishing step beyond the tag. `cmd/celerity-go` is
built and attached to its release by a job of the same workflow, using
`scripts/package-cli.sh`; run it locally with a `TAG` to see what a release
carries:

```bash
TAG=cmd/celerity-go/v0.1.0 bash scripts/package-cli.sh
```

## Tests

Two layers, and the split is about what each can actually establish.

`internal/ipc` covers the protocol, against a stand-in runtime built from the
contract's own generated server stubs and served over a real unix socket. The
bytes on that socket are the bytes on a real one, so the handshake, credit,
cancellation, drain and WebSocket acknowledgement are all exercised for real,
with nothing installed.

`tests/` covers what a stand-in cannot: routing, blueprint parsing and handler
tag construction belong to the runtime, and a stand-in that agreed with the SDK
about them would prove nothing.

```bash
bash scripts/run-tests.sh --with-integration
```

That pulls the pinned `celerity-runtime-core` image, builds
[tests/testapp](./tests/testapp) for the image's platform, and starts the
runtime with the binary and
[tests/fixtures/blueprint.yaml](./tests/fixtures/blueprint.yaml) mounted in.

The runtime **launches the handlers executable itself** rather than connecting
to one, so the application under test lives in the container and the suite
drives it over HTTP. That is why the binary has to be cross-compiled for the
image's architecture, which the script reads from the image rather than
assuming.

The runtime version is pinned in `.env.test`, so a runtime release cannot change
what a test run means without the change being committed.

## Dependency updates

Renovate raises version-update PRs; Dependabot is kept for security updates only. See [docs/RENOVATE.md](./docs/RENOVATE.md).
