# Releasing

How a release of the SDK's modules happens, and why it's built this way. The workflow is
`.github/workflows/release-please.yml`; merging its release pull request is the release.

## One version for every module

Every module is released together at one version, and release-please manages
one package rather than eleven. Its `linked-versions` plugin cannot do this:
the plugin skips any package whose `getComponent()` is empty, which is every
package with `include-component-in-tag` false, and the root module's tag has
to be a bare `vX.Y.Z` or Go cannot find it. So release-please cuts the
repository's tag and `scripts/tag-modules.sh` fans the rest out from the same
commit, named `<directory>/vX.Y.Z` as Go requires. One changelog, at the root,
since a release covers everything. A module requires its siblings at that
version and keeps a `replace` to the checkout alongside, which is what `GOWORK=off` builds
against: a replace in a dependency's go.mod is ignored by whoever requires it,
so the directive is permanent rather than something to strip before release,
and `go.opentelemetry.io/otel` ships the same pair. The consequence is that
the version in a require is never exercised here, so
`scripts/sync-module-versions.sh --check` is what holds it to the manifest.

## Why the release package has no name

The one package carries no `package-name`, and v0.2.1 is what happens when it
does. A merged release pull request is matched back to its package by
component, and the root's is empty because `include-component-in-tag` is
false. The branch is matched separately, by `getBranchComponent`, which reads
the package name and ignores that setting, so a named package expects
`release-please--branches--main--components--celerity-go-sdk` while
`separate-pull-requests` false hands it `release-please--branches--main`. The
mismatch is reported as a warning and the release is declined, which with
eleven packages never arose: a body with more than one release in it is
matched by component alone. So the release ran green, tagged nothing, and left
the pull request pending, which is the state release-please refuses to open
another release in. `check-not-stalled` is what makes that loud. The
`pull-request-title-pattern` went at the same time: `separate-pull-requests`
false means the title comes from `group-pull-request-title-pattern`, so ours
was only producing warnings about the fields it was missing.

## Sibling requirements

The version in a requirement of a sibling module is what consumers resolve
through, and the replace directives mean nothing in this repository exercises
it. So `scripts/sync-module-versions.sh` points each requirement at the
version being released and the `sync-versions` job of the release workflow commits that into the
release pull request, which is the only place it can go: release-please bumps
the manifest without touching go.mod, so a check on the release branch would
fail by construction and block every release. CI checks it everywhere else.

## Publication

Nothing publishes a Go module. A semver tag is the publication, and
`proxy.golang.org` fetches from the repository the first time a version is
asked for, which is also what records it in `index.golang.org` and so what
tells pkg.go.dev the version exists. `scripts/warm-proxy.sh` fetches each
released module after a release, from a scratch module rather than from the
checkout, since a query made inside the repository is answered by the
workspace or by the module being the main one and never reaches the proxy. It
fetches rather than resolving, because a module whose own requirements name a
version that was never tagged resolves perfectly well and then fails for
everyone who builds it, which is how v0.2.0 shipped unusable while every
check was green.
