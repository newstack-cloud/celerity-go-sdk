# Celerity Go SDK

Go SDK for building Celerity applications. Handlers are written once and run either in the
containerised Celerity runtime, over its IPC protocol, or in a provider's serverless
environment through an adapter. The Celerity CLI runs its build tool, `celerity-go`, to
generate platform imports and extract the handler manifest.

## Workspace

This repo is one of NewStack's. NewStack's maintainers develop it alongside the others in a
shared workspace, using the NewStack Claude Code plugin for how work is planned, reviewed,
committed and coordinated across repos. The plugin isn't public, and nothing here depends on it:
this file and the repo's own docs cover what a contribution needs.

## Layout

A Go workspace of eleven modules (`scripts/modules.sh` lists them), split by dependency weight
so that an application that never touches AWS doesn't compile `aws-sdk-go-v2` into its binary.

| Module | Holds |
|---|---|
| `.` (core) | Everything below except the AWS, local, tracing and CLI modules |
| `serverless/aws` | Lambda adapter and event mapping |
| `resources/aws` | S3, SQS, SNS, DynamoDB, RDS, and ElastiCache credentials; a package per resource kind |
| `resources/redis` | The cache, for every platform |
| `resources/local` | The provider a `celerity dev` session runs under, linked by `generate --local` |
| `config/aws`, `config/local` | Config providers: Secrets Manager, Parameter Store, the Lambda extension cache; the Valkey a dev session runs |
| `telemetry/otel`, `resources/redis/otel`, `resources/sqldb/otel` | Tracing, linked by `generate --telemetry otel` or where the blueprint declares a cache or database |
| `cmd/celerity-go` | Build tool: `generate` writes platform imports, `extract` writes the handler manifest |

In core: `celerity/` (app, registration, run), `handler/`, `layer/`, `guard/`, `config/`,
`telemetry/`, `resources/` (provider-agnostic interfaces and handles), `serverless/`,
`manifest/` (handler-manifest.v1), `celeritytest/` (the harness applications test with),
`internal/ipc/` (the IPC client) and `internal/ipcproto/celerityv1/` (generated stubs).
`proto/celerity` is a vendored copy of the runtime's contract.

Why the API and modules are shaped as they are: [docs/DESIGN_DECISIONS.md](docs/DESIGN_DECISIONS.md).
Read the relevant decision before changing a resource contract or a module boundary.

## Checks

Commands are in [CONTRIBUTING.md](CONTRIBUTING.md#commands); each runs over every module.

- **Tools:** Go 1.26, `staticcheck` for lint, `buf` for the protocol scripts, docker for the
  integration run, and `npm install` for the commit-msg hook.
- **Tests:** `bash scripts/run-tests.sh` runs unit tests with `-race` (needs cgo and a C compiler), and
  nothing else.
  `--with-integration` also starts `docker-compose.test-deps.yml` (the pinned
  `celerity-runtime-core` image, Valkey, LocalStack, Postgres, MySQL), builds `tests/testapp`
  for the image, and runs `tests/` and each module's `integration`-tagged suites.
  - It binds host ports 8080, 6379, 4566, 5432 and 3306, so it clashes with any other stack
    on those ports, including Bluelink's test stacks.
  - The runtime version is pinned in `.env.test`.
  - The AWS suites use LocalStack unless `CELERITY_TEST_AWS_EMULATOR=false` is exported; then
    they use the default credential chain and create resources in that account, and leave
    them. Values already exported override `.env.test`'s.
- **Lint:** `bash scripts/lint.sh` (`go vet` and staticcheck), `scripts/check-format.sh`.
- **Protocol:** `scripts/check-proto.sh` checks `proto/celerity` against celerity's `main`
  (fetched over the network) and fails on any difference, reporting whether it's breaking; it
  also checks the committed stubs match.
  `scripts/gen-proto.sh` regenerates them; don't edit `*.pb.go` by hand.
- **Gates:**
  - With `git config core.hooksPath .githooks`, pre-commit checks gofmt on staged Go files and
    commit-msg runs commitlint.
  - CI (`ci.yml`) runs format, proto, vet, lint, build and `run-tests.sh --with-integration`
    on pull requests and pushes to `main`, builds each module with `GOWORK=off`, and runs
    `sync-module-versions.sh --check`.
    `security.yml` runs `govulncheck`; CodeQL runs too.

## Compatibility

- **Public Go API:** pre-1.0 (`v0.x`); a breaking change bumps the minor version. Resource
  contracts are kept at parity with the Node and Python SDKs (`docs/DESIGN_DECISIONS.md`
  names where they differ); a contract change says what each other SDK needs.
- **The Celerity CLI's interface:** `celerity-go generate` and `extract`, every flag of each
  (the CLI passes `--target`, `--local`, `--sql-engine`, `--telemetry`, `--package`,
  `--resource`, `--test-packages` and `--out`; see celerity
  `apps/cli/internal/handlertoolchain/golang.go`), and the handler-manifest.v1 format. Additive
  only; anything else needs a matching change in the Celerity CLI first.
- **IPC protocol:** owned by celerity's `libs/runtime/proto`. Change it there, then re-vendor.
- **Persisted state:** none.

## Conventions

- Commits: conventional, scopes in `commitlint.config.js`. Every module shares one release
  whatever the scope. Only `feat`, `fix`, `perf` and `deps` release and appear in the
  changelog, so use another type (`ci`, `docs`, `chore`, `refactor`, `test`) for changes that
  don't change what a module does.
- Releases: every module at one version, through one release pull request
  ([docs/RELEASING.md](docs/RELEASING.md)). Don't edit `CHANGELOG.md`, the manifest, sibling
  `require` versions or tags by hand; the release workflow sets them.
- A module that requires a sibling keeps a `replace` to the sibling's checkout. It's
  permanent; don't remove it.
- Complexity 15 or less, nesting 3 or less, lines under 120 characters.
- Doc comments in godoc form on exported API; on unexported code only for a constraint or
  rationale the code can't show. Tests go through the public API, table-driven, with cases
  named for the behaviour.
- Dependency updates: Renovate for versions, Dependabot for security only, so there's no
  `.github/dependabot.yml` ([docs/RENOVATE.md](docs/RENOVATE.md)).

## Safety

- Don't run the integration suites against a real AWS account unless asked. Before
  `--with-integration`, check `CELERITY_TEST_AWS_EMULATOR` isn't exported as `false`.
- Don't create tags or releases, or run `tag-modules.sh`, which pushes every local tag
  (`git push origin --tags`), or `warm-proxy.sh`.

## Known mistakes

<!-- One line per mistake an agent has made here, phrased as the rule that prevents it. -->
