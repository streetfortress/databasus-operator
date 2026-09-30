# Roadmap

## Current state (July 2026)

The operator was developed and tested against **databasus v3.38.0**. It reconciles
`Storage`, `Notifier`, and `DatabaseBackup` CRs against the databasus REST API via
the client in `internal/client/`. Lint is clean; there is no automated test coverage
of the API client yet — that is the top gap.

## Design principle: operator-owned CRD schema

databasus does not guarantee API stability, and it ships breaking renames every few
minor versions. To keep that churn out of user-facing CRs, **the operator owns the
CRD vocabulary** and translates to the pinned upstream version's wire format inside
`internal/client/`. Concretely:

- The CRD keeps `spec.database.type: POSTGRES` and `spec.database.postgresql`;
  the client maps these to upstream's `POSTGRES_LOGICAL` / `postgresqlLogical`.
- Upstream renames (e.g. interval `interval` → `type`) are absorbed as code changes
  to the client DTOs, never as CR schema changes.
- New *capabilities* (not renames) may still change the CRD — see `sslMode` below.

While the API is `v1alpha1`, schema breaks are handled **in place**: bump nothing,
document the break, recreate affected CRs. Conversion webhooks / version bumps are
deferred until there are external users.

## 1. Migrate to databasus v3.48.x

Audit of upstream changes between v3.38.0 (fork point `41de3b7c`, 2026-05-12) and
v3.48.0, verified against the v3.48.0 source (July 2026):

| Endpoint area | Verdict | Detail |
|---|---|---|
| `POST /api/v1/users/signin` | unchanged | still `email`/`password` → `token`; Bearer auth unchanged |
| `GET /api/v1/workspaces` | unchanged | |
| `GET /api/v1/system/health` | unchanged | |
| `/api/v1/notifiers` | unchanged | all six notifier type DTOs unchanged |
| `/api/v1/healthcheck-config` | unchanged | |
| `/api/v1/storages` | near-unchanged | `isSystem` removed from the storage model; we send it in `StorageRequest` (not the response as previously noted) — silently ignored, drop it |
| `/api/v1/databases/*` | **breaking** | type enum `POSTGRES` removed, replaced by `POSTGRES_LOGICAL` / `POSTGRES_PHYSICAL` (no back-compat alias); `postgresql` field split into `postgresqlLogical` / `postgresqlPhysical`. The logical DTO also **drops `isHttps` and `backupType`** and adds `sslMode` (`disable`/`require`/`verify-ca`/`verify-full`), `sslClientCert`/`sslClientKey`/`sslRootCert`, `excludeTables`, `isSkipUserMappings`. Routes themselves unchanged. `isHttps` survives for mysql/mariadb/mongodb |
| `/api/v1/backup-configs/*` | near-unchanged for us | our existing paths (`POST /backup-configs/save`, `GET /backup-configs/database/{id}`) **are** the logical paths; only the physical variants got new `/backup-configs/physical/...` routes (unused). Interval DTO renamed `interval` → `type` and dropped `id` |

Note: the server does not reject unknown JSON fields, so stale fields fail silently
— against v3.48 the old `postgresql` payload is dropped wholesale and creation fails
with a validation error ("host is required"), not a schema error.

Discovered by the contract tests (July 2026): v3.48 reports missing records as
**400 with gorm's "record not found" text, not 404** (no controller in scope uses
`StatusNotFound`). The client's `isNotFound` helper treats both as absence, and the
Delete methods treat not-found as success so finalizers cannot wedge on records
deleted out-of-band. `POST /databases/update` also requires `workspaceId`; the
client now defaults it on update as it already did on create.

### Decisions (July 2026)

- **CRD stays operator-owned** (see principle above): `type: POSTGRES` and the
  `postgresql` key are kept; the client layer does the renaming.
- **`backupType` (`PG_DUMP`/`WAL_V1`) is kept** as the operator's logical/physical
  selector. `PG_DUMP` maps to `POSTGRES_LOGICAL`; `WAL_V1` is rejected by the
  controller with a clear error until physical backups are supported.
- **`isHttps` is replaced by `sslMode` + cert secret refs** on the postgres spec —
  this is new upstream capability, not a rename, so it does change the CRD.
  Cert material follows the existing `SecretKeyRef` pattern
  (`sslClientCertSecretRef`, `sslClientKeySecretRef`, `sslRootCertSecretRef`).
  Breaking for CRs with `isHttps: true`; test-env CRs are recreated.
- **Client migration and the contract-test suite land on the same branch**, pinned
  at v3.48, so the README compatibility claim is proven at merge, not asserted.

### Tasks

- [x] `internal/client/database.go`: map `POSTGRES` → `POSTGRES_LOGICAL`; replace
      `PostgresqlRequest` with the `postgresqlLogical` DTO (`sslMode`, cert fields,
      `excludeTables`, `isSkipUserMappings`; no `isHttps`/`backupType`)
- [x] `internal/client/backup_config.go`: `IntervalRequest` field `interval` → `type`,
      drop `id`
- [x] `internal/client/storage.go`: drop `IsSystem` from `StorageRequest`
- [x] CRD: replace `PostgresqlDatabaseSpec.IsHttps` with `sslMode` (+ optional
      cert secret refs, `excludeTables`, `isSkipUserMappings`); reject
      `backupType: WAL_V1` in the controller; `make manifests generate`
- [x] Contract tests (see §2) in the same branch, pinned to v3.48
      (`make test-contract`, `test/contract/`)
- [x] Update the README compatibility matrix; document the `isHttps` → `sslMode`
      CR break and test-env CR recreation
- [x] Recreate the test-env CRs against the new CRD schema and verify a live
      reconcile end-to-end (2026-08-05, against databasus v3.51.0 on luxor:
      Storage + DatabaseBackup synced, gitea pg_dump landed in the rustfs
      `databasus` bucket, healthcheck reports AVAILABLE)

### v3.48.0 → v3.51.0 re-audit (August 2026)

The fleet deployed databasus v3.51.0 (sfi/deployments#69), so the audit was
re-run against the v3.48.0..v3.51.0 source diff: **no changes on any endpoint
the operator uses**. `backup-configs`, `storages` (S3), `workspaces`, `users`,
and `healthcheck-config` are untouched; `databases` changes are internal
nil-guards; `notifiers` gained additive notification types (webhook
`acceptNotificationTypes` filtering). New upstream work (physical-backup
recovery scripts, logical DB size estimation, telemetry) is off our paths.
The contract pin moved straight to v3.51.0 and the suite passes unchanged.

## 2. Contract tests in CI

Make the compatibility matrix verified instead of asserted:

- [x] Integration test suite that runs the `internal/client` calls against a real
      databasus instance (pinned image tag + postgres) started in CI
      (`test/contract/`, `make test-contract`, CI `contract` job)
- [x] Pin the databasus image version in one greppable place so Renovate can bump it;
      each upstream release then arrives as a PR whose CI proves (or disproves)
      compatibility
- [ ] Internal-cluster e2e (operator deployed alongside the upstream Helm chart)
      as a Gitea-only workflow

## 3. Release engineering

- [x] Release workflow (August 2026, `.github/workflows/release.yml`;
      reshaped September 2026 after oss/s3-exporter's, #14): semver tag →
      multi-arch image to `ghcr.io/<owner>/databasus-operator`, Helm chart to
      `oci://ghcr.io/<owner>/charts`, `install.yaml` attached to the GitHub
      release, whose body is the commit subjects since the previous tag. The
      owner comes from `github.repository_owner`, so another transfer of the
      repository needs no edit here; the three jobs chain with `needs`, and
      `contents: write` sits on the release job alone. Runs on GitHub only —
      the ref-pusher of sfi/deployments (#612) pushes main and the v* tags
      there, and that push carries a GitHub App installation token rather
      than a workflow's GITHUB_TOKEN, so it triggers the workflow; the
      installation token needs `workflows: write`, and first-push GHCR
      packages must be flipped to public in the web UI. `just tag X.Y.Z`
      guards the tagging (semver check, HEAD must be origin/main); pushing
      the tag is the whole release — there is no by-hand pipeline, since
      nothing here is CI-hostile the way PrimeTime's Mac-bound app release
      is.
- [x] Helm chart (`charts/databasus-operator/`): lockstep chart version =
      appVersion = release tag, stamped at package time; CRDs render as
      templates (synced from `config/crd` via `make chart-crds`, enforced in
      CI) so `helm upgrade` keeps them current.
- [x] Cut v0.1.0 (September 2026) and pin the fleet to it
      (`fleet/apps/luxor/databasus-central/` in sfi/deployments)
- [x] Re-home to streetfortress (September 2026, #14, sfi/deployments#612):
      module path `github.com/streetfortress/databasus-operator`, image
      `ghcr.io/streetfortress/databasus-operator`, chart
      `oci://ghcr.io/streetfortress/charts`. v0.1.0 and its two packages stay
      under the old `sf1tzp` names — nothing republishes them, and the fleet
      moves to the new names in sfi/deployments#518 after the first release
      here. The first tag after the re-home needs the two new GHCR packages
      flipped to public.
- [ ] Versioned compatibility rows in the README (operator vX.Y ↔ databasus vA.B)

## 4. Later ideas

- Runtime version check: warn (via a status condition) when the connected databasus
  version is outside the tested range
- Physical backups: `backupType: WAL_V1` → `POSTGRES_PHYSICAL` + the
  `/backup-configs/physical/...` endpoints (requires the extra retention/interval
  fields the physical config demands)
- Surface upstream's backup-verification feature (v3.34+) in the CRDs
- Helm chart for the operator itself
