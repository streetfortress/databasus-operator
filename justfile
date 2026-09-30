# Project scripts live here; Go and chart build targets stay in the
# Makefile (kubebuilder convention — lint/test/build/manifests/chart-crds).
#
# There is deliberately no `release` recipe: pushing the tag IS the release.
# The ref-pusher of sfi/deployments pushes the tag to GitHub within five
# minutes, and there .github/workflows/release.yml publishes the multi-arch
# image, the chart, and install.yaml. A failed run is re-run from the GitHub
# UI. (PrimeTime needs a by-hand pipeline because its app release is
# Mac-bound; nothing here is.)

# Accepts "1.2.3" or "v1.2.3" — any leading v is stripped before re-adding,
# so "vv1.2.3" can't happen. Enforces vX.Y.Z (no prerelease/build, matching
# the release workflow's trigger). Fetches first (pruning tags deleted on
# the remote) and refuses to tag unless HEAD is exactly origin/main, so a
# stale checkout can't ship a release.
#
# Tag HEAD as vX.Y.Z and push it — the ref-pusher carries the tag to GitHub,
# where it triggers the release CI.
tag version:
  #!/usr/bin/env bash
  set -euo pipefail
  v="{{version}}"
  v="${v#v}"
  [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] \
      || { echo "error: 'v$v' is not vX.Y.Z semver" >&2; exit 1; }
  git fetch origin --tags --prune --prune-tags
  [[ "$(git rev-parse HEAD)" == "$(git rev-parse origin/main)" ]] \
      || { echo "error: HEAD is not at origin/main — pull first" >&2; exit 1; }
  git tag "v$v" && git push origin "v$v"
