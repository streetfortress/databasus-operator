# databasus-operator — Agent Context

A standalone Kubernetes operator (kubebuilder v4) that reconciles `Storage`,
`Notifier`, and `DatabaseBackup` CRs against the REST API of a stock
[databasus](https://github.com/databasus/databasus) instance. It is **not** a fork
of databasus — it deploys alongside the upstream Helm chart and drives the same API
the web UI uses.

## Repo model

- **origin** (primary): `ssh://git@gitea.zen.lofi:30022/oss/databasus-operator.git`
- `github.com/streetfortress/databasus-operator` is the **public face** of origin —
  never push to it directly, and never merge community PRs on GitHub. Fetch the PR
  branch, merge on gitea (regular merge, not squash/rebase, so GitHub auto-marks the
  PR merged when the commits reach it).
- The ref-pusher of sfi/deployments (#612) publishes the refs, not a gitea push
  mirror: a CronJob pushes only what its allowlist names — `refs/heads/main` and
  `refs/tags/v*`. A branch you push to gitea therefore stays private, and a tag
  reaches GitHub within five minutes. A diverged GitHub ref fails the push instead
  of being overwritten; the `RefPusherLineFailing` alert reports it.
- The repo was `sf1tzp/databasus-operator` on GitHub and `ghcr.io/sf1tzp` on GHCR
  until September 2026 (#14). `v0.1.0` and its two packages still live under the
  old names; every release after it goes to `streetfortress`.
- The Go module path is the GitHub path (public identity), even though development
  happens on gitea.
- CI lives in `.github/workflows/` and runs on both Gitea Actions and GitHub Actions.
  Caveat: Gitea only falls back to `.github/workflows` while `.gitea/workflows`
  does not exist — the first gitea-only workflow added there requires copying
  `ci.yml` in as well.

## Upstream compatibility

databasus's API is explicitly unstable. Every change that touches
`internal/client/` must keep the README compatibility matrix truthful, and the
pinned databasus version used by tests is the source of truth for what "tested"
means. Current target and the v3.38 → v3.48 migration plan: see
[docs/ROADMAP.md](docs/ROADMAP.md).

## Commands

- `make lint` — golangci-lint (keep it clean; it was cleaned up in July 2026)
- `make test` — envtest-based tests
- `make build` — manager binary; `make manifests generate` after API type changes

## History

Extracted July 2026 from a databasus fork (`~/oss/databasus-operator`, now an
archive) via `git subtree split -P operator`. The fork's upstream PR was rejected
(maintainer priorities + API instability), which is why this exists as a separate
project.
