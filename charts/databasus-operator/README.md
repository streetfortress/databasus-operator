# databasus-operator Helm chart

Deploys the [databasus-operator](https://github.com/streetfortress/databasus-operator)
manager alongside an existing [databasus](https://github.com/databasus/databasus)
instance. Check the operator README's compatibility matrix before pairing
versions — databasus's API is not stable.

## Install

```sh
helm install databasus-operator oci://ghcr.io/streetfortress/charts/databasus-operator \
  --namespace databasus \
  --set databasus.apiUrl=http://databasus-service.databasus.svc.cluster.local:4005
```

The operator authenticates with an existing databasus user. Create the
credentials Secret first (or set `databasus.credentials.create=true` with
`email`/`password` values for test environments):

```sh
kubectl -n databasus create secret generic databasus-operator-credentials \
  --from-literal=email=<user> --from-literal=password=<password>
```

## Values

| Key | Default | Purpose |
|-----|---------|---------|
| `databasus.apiUrl` | upstream chart's in-cluster service URL | databasus API base URL |
| `databasus.credentials.secretName` | `databasus-operator-credentials` | Secret with `email`/`password` (optional `workspaceName`/`workspaceId`) |
| `databasus.credentials.secretNamespace` | release namespace | Where that Secret lives |
| `databasus.credentials.create` | `false` | Render the Secret from chart values (test envs only) |
| `image.repository` / `image.tag` | `ghcr.io/streetfortress/databasus-operator` / `v<appVersion>` | Manager image |
| `crds.enabled` | `true` | Install/upgrade CRDs with the chart. **Uninstalling then deletes all operator CRs** — disable if CRDs are managed out-of-band |
| `leaderElection` | `true` | Leader election (plus its Role/RoleBinding) |
| `replicaCount` | `1` | Manager replicas (only one leads) |

Versioning is lockstep: chart version = appVersion = the repo release tag,
stamped by the release workflow at package time.
