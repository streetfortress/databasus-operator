# databasus-operator

[![CI](https://github.com/streetfortress/databasus-operator/actions/workflows/ci.yml/badge.svg)](https://github.com/streetfortress/databasus-operator/actions/workflows/ci.yml)

> NOTICE: Big changes are coming soon. TLDR; databasus is moving in the opposite direction from our desired use case, so we'll be replacing this operator's backend with a focused, CNPG-aware, backup reconciler instead of operating a databasus client programatically. Big thanks to the databasus authors for providing the inspiration for this project!

A Kubernetes operator that manages [databasus](https://github.com/databasus/databasus) configuration declaratively via Custom Resource Definitions (CRDs). Instead of configuring databases, backups, storages, and notifiers through the web UI, define them as Kubernetes resources and let the operator reconcile them against the databasus API.

The operator is a standalone project — it is not a fork or a modified distribution of databasus. Deploy it alongside a stock databasus instance (e.g. the [upstream Helm chart](https://github.com/databasus/databasus/tree/main/deploy/helm)) and it drives configuration through the same REST API the web UI uses.

## Compatibility

databasus does not yet guarantee a stable API, so each operator release is pinned to the databasus versions it was tested against:

| operator | databasus | status |
|----------|-----------|--------|
| `main`   | v3.51.0   | tested |
| `main`   | v3.48.0 – v3.50.0 | compatible (audited; contract-tested at v3.48.0) |

If you run an untested databasus version, the operator may fail to reconcile after upstream API changes — check this table before upgrading databasus. `main` no longer speaks the pre-v3.48 wire format (v3.48 split the postgres type into logical/physical variants); use an older operator commit for databasus ≤ v3.47.

**CR schema break (v3.48 migration):** `DatabaseBackup.spec.database.postgresql.isHttps` was replaced by `sslMode` (`disable`/`require`/`verify-ca`/`verify-full`) plus optional `sslClientCertSecretRef`/`sslClientKeySecretRef`/`sslRootCertSecretRef` (each a Secret name/key reference). CRs that set `isHttps: true` should now set `sslMode: require`. Re-apply the CRDs and recreate affected `DatabaseBackup` resources.

**Scope: logical backups only.** The operator drives databasus's logical (`pg_dump`-style) backups. databasus's physical postgres backups (`POSTGRES_PHYSICAL`, WAL streaming) are unsupported — `backupType: WAL_V1` is rejected with a `Ready=False` condition. If you need physical/WAL-based backups, use a mechanism native to your database platform instead, e.g. CloudNativePG's barman plugin with WAL archiving to S3, or create an Issue on Github. With enough community interest we may add this in the future.

## How it works

The operator watches three CRDs and syncs their state to a running databasus instance via its REST API:

- **`Storage`** -- storage backends where backups are saved (S3, SFTP, Azure Blob, etc.)
- **`Notifier`** -- notification channels for alerts (Discord, Slack, Telegram, etc.)
- **`DatabaseBackup`** -- database connection + backup schedule + healthcheck config, referencing Storage and Notifier resources by name

On create/update, the operator calls the databasus API to upsert resources. On delete, a finalizer ensures cleanup via the API before the Kubernetes resource is removed.

## Prerequisites

- A running databasus instance accessible from the cluster
- `kubectl` configured for your cluster
- `make`, `go 1.25+`, `docker` (for building)

## Setup

### 1. Create the credentials Secret

The operator authenticates to databasus using an email/password stored in a Kubernetes Secret. It signs in on startup to get a JWT token and resolves the workspace automatically.

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: databasus-operator-credentials
  namespace: databasus
type: Opaque
stringData:
  email: admin@example.com
  password: your-password
  # Optional: target a specific workspace by name (defaults to first available)
  # workspaceName: "My Workspace"
  # Optional: or by UUID (takes precedence over workspaceName)
  # workspaceId: "xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
```

```bash
kubectl apply -f credentials-secret.yaml
```

### 2. Create Secrets for your resources

Each CRD references sensitive values via `secretKeyRef` fields pointing to Kubernetes Secrets. Create these before applying the CRDs.

```bash
# Storage credentials (e.g., S3)
kubectl create secret generic rustfs-credentials \
  --from-literal=access-key=YOUR_ACCESS_KEY \
  --from-literal=secret-key=YOUR_SECRET_KEY \
  -n databasus-operator-system

# Notifier credentials (e.g., Discord webhook)
kubectl create secret generic discord-webhook \
  --from-literal=url=https://discord.com/api/webhooks/... \
  -n databasus-operator-system

# Database password
kubectl create secret generic gitea-db-credentials \
  --from-literal=password=YOUR_DB_PASSWORD \
  -n databasus-operator-system
```

### 3. Install the operator

Every release tag publishes a multi-arch image (`ghcr.io/streetfortress/databasus-operator`) and a Helm chart to GHCR. Chart version, appVersion, and image tag move in lockstep with the release.

> `v0.1.0` predates the move to the `streetfortress` org and is published under the old names — `ghcr.io/sf1tzp/databasus-operator` and `oci://ghcr.io/sf1tzp/charts`. Every release after it uses the names below.

```bash
helm install databasus-operator oci://ghcr.io/streetfortress/charts/databasus-operator \
  --namespace databasus --create-namespace
```

See the [chart README](charts/databasus-operator/README.md) for values — databasus API URL, credentials Secret, and CRD handling (CRDs upgrade with the chart by default; uninstalling the chart then removes them and every operator CR).

Alternatively, apply the standalone manifest attached to each [GitHub release](https://github.com/streetfortress/databasus-operator/releases):

```bash
kubectl apply -f https://github.com/streetfortress/databasus-operator/releases/download/vX.Y.Z/install.yaml
```

Or build and deploy from source:

```bash
# Build the image
make docker-build IMG=databasus-operator:latest

# Import into k3s (if using local images)
docker save databasus-operator:latest | sudo k3s ctr images import -

# Install CRDs and deploy the operator
make install
make deploy IMG=databasus-operator:latest
```

### 4. Apply your resources

```bash
kubectl apply -f config/samples/storage_s3.yaml
kubectl apply -f config/samples/notifier_discord.yaml
kubectl apply -f config/samples/databasebackup_gitea.yaml
```

### 5. Verify

```bash
kubectl get storages,notifiers,databasebackups -n databasus-operator-system
```

```
NAME                                    TYPE   READY   AGE
storage.databasus.databasus.io/rustfs   S3     True    5m

NAME                                                TYPE      READY   AGE
notifier.databasus.databasus.io/operator-discord    DISCORD   True    5m

NAME                                              DB TYPE    HEALTH      READY   LAST BACKUP   AGE
databasebackup.databasus.databasus.io/gitea       POSTGRES   AVAILABLE   True    <timestamp>   5m
```

## CRD Reference

### Storage

Defines a backup storage backend. Supported types: `S3`, `SFTP`, `AZURE_BLOB`, `LOCAL`, `FTP`, `RCLONE`, `NAS`, `GOOGLE_DRIVE`.

```yaml
apiVersion: databasus.databasus.io/v1alpha1
kind: Storage
metadata:
  name: my-s3-storage
  namespace: databasus
spec:
  name: my-s3-storage
  type: S3
  s3:
    bucket: my-backup-bucket
    region: us-east-1
    endpoint: http://minio.minio.svc.cluster.local:9000  # optional for AWS
    prefix: backups/  # optional
    accessKeySecretRef:
      name: s3-credentials
      key: access-key
    secretKeySecretRef:
      name: s3-credentials
      key: secret-key
    isSkipTLSVerify: false  # optional
    storageClass: STANDARD  # optional
```

### Notifier

Defines a notification channel. Supported types: `DISCORD`, `SLACK`, `TELEGRAM`, `EMAIL`, `WEBHOOK`, `TEAMS`.

```yaml
apiVersion: databasus.databasus.io/v1alpha1
kind: Notifier
metadata:
  name: my-discord
  namespace: databasus
spec:
  name: My Discord Channel
  type: DISCORD
  discord:
    webhookURLSecretRef:
      name: discord-webhook
      key: url
```

### DatabaseBackup

The main resource combining database connection, backup configuration, and health checks. References Storage and Notifier CRDs by their metadata name.

```yaml
apiVersion: databasus.databasus.io/v1alpha1
kind: DatabaseBackup
metadata:
  name: my-database
  namespace: databasus
spec:
  database:
    name: my-database
    type: POSTGRES  # POSTGRES, MYSQL, MARIADB, MONGODB
    notifierRefs:
      - my-discord  # metadata.name of a Notifier CRD
    postgresql:
      version: "17"
      host: postgres.default.svc.cluster.local
      port: 5432
      username: myuser
      passwordSecretRef:
        name: db-credentials
        key: password
      database: mydb
      backupType: PG_DUMP  # only PG_DUMP is supported (see "Scope" above)

  backup:
    isEnabled: true
    storageRef: my-s3-storage  # metadata.name of a Storage CRD
    encryption: ENCRYPTED      # NONE or ENCRYPTED
    interval:
      type: DAILY              # HOURLY, DAILY, WEEKLY, MONTHLY, CRON
      timeOfDay: "09:00"
    retentionPolicy:
      type: TIME_PERIOD        # TIME_PERIOD, COUNT, GFS
      timePeriod: "90d"
    isRetryIfFailed: true
    maxFailedTriesCount: 3
    sendNotificationsOn:
      - BACKUP_FAILED          # BACKUP_FAILED, BACKUP_SUCCESS

  healthcheck:
    isEnabled: true
    isSentNotificationWhenUnavailable: true
    intervalMinutes: 1
    attemptsBeforeConsideredAsDown: 3
    storeAttemptsDays: 7
```

## Configuration

The operator reads its configuration from environment variables set in the deployment:

| Variable | Default | Description |
|----------|---------|-------------|
| `DATABASUS_API_URL` | `http://databasus-service.databasus.svc.cluster.local:4005` | databasus API endpoint |
| `DATABASUS_CREDENTIALS_SECRET` | `databasus-operator-credentials` | Name of the credentials Secret |
| `DATABASUS_CREDENTIALS_NAMESPACE` | `databasus` | Namespace of the credentials Secret |

## Uninstall

```bash
# Remove CRs (triggers finalizer cleanup via databasus API)
kubectl delete databasebackups,notifiers,storages --all -n databasus-operator-system

# Remove operator and CRDs
make undeploy
make uninstall
```

## Development

Primary development happens on a private Gitea instance, which publishes `main` and the release tags to this GitHub repository. Issues and pull requests are welcome on GitHub — PRs are fetched and merged internally, and the merge commit then appears here.

```bash
make lint   # golangci-lint
make test   # unit tests via envtest
make build  # manager binary
```

Releasing is a single guarded step — pushing the tag is the release (the tag reaches GitHub and triggers the publish workflow there):

```bash
just tag 0.1.0   # tag HEAD (must be at origin/main) and push
```

## License

Apache-2.0 — see [LICENSE](LICENSE).
