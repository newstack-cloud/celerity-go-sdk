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
fix(core): grant credit when a handler panics
docs: describe the two-pass extraction
```

**Types**: `feat`, `fix`, `build`, `revert`, `wip`, `chore`, `ci`, `docs`,
`style`, `refactor`, `perf`, `test`, `instr`, `deps`

**Scopes**: one per module, so a scope names the module a commit changes and
nothing finer. `commitlint.config.js` holds the list.

| Scope | Module | Import path |
|---|---|---|
| `core` | `.` | `github.com/newstack-cloud/celerity-go-sdk` |
| `serverless-aws` | `serverless/aws` | `.../serverless/aws` |
| `resources-aws` | `resources/aws` | `.../resources/aws` |
| `resources-redis` | `resources/redis` | `.../resources/redis` |
| `resources-local` | `resources/local` | `.../resources/local` |
| `resources-redis-otel` | `resources/redis/otel` | `.../resources/redis/otel` |
| `resources-sqldb-otel` | `resources/sqldb/otel` | `.../resources/sqldb/otel` |
| `telemetry-otel` | `telemetry/otel` | `.../telemetry/otel` |
| `config-aws` | `config/aws` | `.../config/aws` |
| `config-local` | `config/local` | `.../config/local` |
| `cli` | `cmd/celerity-go` | `.../cmd/celerity-go` |
| `deps` | — | dependency updates belonging to no module |

A scope naming a package rather than a module would drift as packages move
between modules.

The scope is optional. A repository-wide change that belongs to no module is
better without one: `chore: ignore the built binary` rather than an invented
scope for it.

## Releases

Every module releases together, at one version. release-please keeps one release pull request
for the repository; merging it tags the root module (`v0.2.1`), and `scripts/tag-modules.sh`
tags every other module at the same version with its directory prefix
(`serverless/aws/v0.2.1`), as Go requires. There is one changelog, at the root.

Every module shares that release, whatever a commit's scope. Only `feat`, `fix`, `perf` and
`deps` release and appear in the changelog, so use another type (`ci`, `docs`, `chore`,
`refactor`, `style` or `test`) for changes that don't change what a module does.

CI builds each module with `GOWORK=off`, against its own `go.mod` and `replace` directives
rather than the workspace.

Library modules need no publishing step beyond the tag. `cmd/celerity-go` is built and
attached to its release by a job of the same workflow, using `scripts/package-cli.sh`; run it
locally with a `TAG` to see what a release carries:

```bash
TAG=cmd/celerity-go/v0.1.0 bash scripts/package-cli.sh
```

Why it works this way, and what broke the releases that didn't: [docs/RELEASING.md](docs/RELEASING.md).

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

### Where the AWS suites point

The AWS suites do not name an emulator. They read a target from the environment, so the
same suites run against the emulator compose brings up or against an account:

| Variable | Default | What it does |
|---|---|---|
| `CELERITY_TEST_AWS_EMULATOR` | `true` | Invents the credential an emulator does not check, and supplies the default endpoint. `false` leaves credentials to the SDK's default chain, so a profile or an assumed role |
| `CELERITY_TEST_AWS_ENDPOINT` | `http://127.0.0.1:4566` when emulating, otherwise unset | Where the SDK is pointed. Unset against an account, so each service's own endpoint is resolved |
| `CELERITY_TEST_AWS_REGION` | `eu-west-2` | The region clients are built for and signatures are scoped to |
| `CELERITY_TEST_AWS_RESOURCE_PREFIX` | empty | Goes in front of every resource name a suite creates, so two runs sharing an account do not share a table |

An unset or unreadable `CELERITY_TEST_AWS_EMULATOR` is an emulator, because the
mistake worth preventing is a suite creating tables in an account nobody meant
it to reach.

The defaults live in `.env.test`, which `run-tests.sh` reads without overwriting
anything already exported, so pointing a run elsewhere is a matter of exporting
the variables above first.

Against an account, set a prefix. The suites create the resource a deployment
would have created and leave it, which is what makes a rerun cheap against an
emulator and is why two unprefixed runs in one account would collide.

The same run brings up what the provider modules read, a Valkey for the local
config provider and the cache, an AWS emulator for the AWS services, along with a Postgres instance
and a MySQL instance for the RDS suite. RDS has no emulator, and what is worth covering
there is the connection string, the pool and the driver seam rather than
anything AWS does, all of which a real database handles. Both engines are included, because a
connection string is built differently for each and nothing but a server
refusing one says it was built wrongly.

The drivers that suite imports are test dependencies, so applications still
carry none, and they are also what pins the versions `celerity-go generate`
writes imports of.

## Dependency updates

Renovate raises version-update PRs; Dependabot is kept for security updates only. See [docs/RENOVATE.md](./docs/RENOVATE.md).
