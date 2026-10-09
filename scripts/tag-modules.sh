#!/usr/bin/env bash
set -euo pipefail

# Tags every module in this repository at the version release-please released.
#
# release-please manages one package, which is the repository, so it creates one
# tag. Go needs one per module, named for the directory the module is in, and a
# module in a subdirectory is only fetchable at <subdir>/vX.Y.Z. So the rest are
# created here, all on the commit the release was cut from.
#
# One package rather than eleven because every module is released together.
# release-please can link several packages to one version, but not a package
# whose tag carries no component, and the repository's root module has to be
# tagged vX.Y.Z for Go to find it at all. So the version is the repository's and
# the tags fan out from it.
#
# TAG is the tag release-please created, which names the version.

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
source "$SCRIPT_DIR/modules.sh"

cd "$ROOT"

if [ -z "${TAG:-}" ]; then
  echo "no tag: set TAG to the one release-please created" >&2
  exit 1
fi

# The commit the release was cut from, so every module names the same source.
commit=$(git rev-list -n 1 "$TAG")
version="${TAG#v}"

echo "tagging every module at v$version, on $commit"

for module in "${MODULES[@]}"; do
  # The root is the tag release-please already created.
  [ "$module" = "." ] && continue

  tag="$module/v$version"
  if git rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
    echo "  $tag exists already"
    continue
  fi

  git tag "$tag" "$commit"
  echo "  $tag"
done

# Pushed together, so a release is never half-tagged, a consumer resolving one
# module's requirement of another needs both to be there.
git push origin --tags
