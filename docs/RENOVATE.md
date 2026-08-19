# Dependency Updates

Renovate owns version updates. Dependabot is kept for security updates only.

The two overlap almost entirely, and running both for version updates means two
bots opening two PRs for the same bump. Splitting them by purpose keeps one
tool's strengths in each half: Renovate groups, schedules and applies rules
across a multi-module repository far better, and Dependabot's security updates
are wired directly into GitHub's advisory database and alerting.

## Renovate: version updates

Configured in [renovate.json](../renovate.json), extending the shared
`newstack-cloud/renovate-config` preset, and run by
[.github/workflows/renovate.yml](../.github/workflows/renovate.yml) as a
self-hosted runner on an hourly schedule.

What the repository's own rules add on top of the preset:

| Rule | Why |
|---|---|
| This repository's own modules are disabled | release-please owns their versions; a Renovate bump would race the release PR that produces one |
| Go minor and patch grouped weekly, `deps:` | A shared dependency moves in every module at once, so the PR is about the dependency rather than any one module |
| Go major updates ungrouped | Each needs reading rather than merging |
| grpc and protobuf grouped, `deps(proto):` | They move with the checked-in stubs: a bump that changes generated output needs `scripts/gen-proto.sh` run in the same PR, which CI enforces |
| Actions grouped, `ci(deps):` | A workflow pin is not a dependency of the software |
| The `go` directive and toolchain disabled | A deliberate choice across every module, not something to raise a PR for whenever a patch release lands |

Commit types and scopes are constrained by
[commitlint.config.js](../commitlint.config.js), so a rule that emits a scope
outside that list produces PRs that fail their own commit hook. Adding a rule
with a new scope means adding the scope there too.

## Dependabot: security updates only

There is deliberately **no `.github/dependabot.yml`**. That file configures
version updates, which is Renovate's job here; adding one back would restore the
duplication this split exists to remove.

Security updates need no config file. They come from repository settings:

- Settings → Advanced Security → **Dependabot alerts**: enabled
- Settings → Advanced Security → **Dependabot security updates**: enabled

With those on, Dependabot raises a PR only when an advisory affects a resolved
dependency, across every `go.mod` in the repository, whatever Renovate's
schedule happens to be. That is the property worth keeping: a security fix
should not wait for a weekly grouping window.

`govulncheck` in [the CI and security workflows](../.github/workflows/security.yml)
covers the same ground from the other direction, reporting only vulnerabilities
on a code path the binary actually reaches, so a dependency advisory that this
code cannot trigger is visible without being urgent.
