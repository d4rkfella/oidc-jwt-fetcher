# OIDC JWT Fetcher CronJob

This application runs as a Kubernetes CronJob. It fetches an OIDC JWT token using the client credentials grant and then creates/updates a Kubernetes Secret with this token in specified Kubernetes namespaces.

## Functionality

The application can operate in two modes for namespace targeting, controlled by the `TARGET_NAMESPACES` environment variable:

1.  **All Namespaces Mode**: If `TARGET_NAMESPACES` is not set or is empty, the application attempts to:
    *   Fetch an OIDC JWT token.
    *   List all namespaces in the Kubernetes cluster.
    *   For each listed namespace, create (or update) a Kubernetes Secret containing the fetched JWT.
    *   *This mode requires cluster-wide permissions to list namespaces.*

2.  **Specific Namespaces Mode**: If `TARGET_NAMESPACES` is set to a comma-separated list of namespace names (e.g., "ns1,ns2,my-app"), the application attempts to:
    *   Fetch an OIDC JWT token.
    *   For each specified namespace in the list, create (or update) a Kubernetes Secret containing the fetched JWT.
    *   *This mode does not require cluster-wide permission to list all namespaces. Permissions for secret operations can be scoped to the specified namespaces.*

In both modes, if a secret operation fails in a particular namespace (e.g., due to RBAC restrictions not allowing secret creation/update in that namespace), the application will currently log a fatal error and terminate. Future enhancements could allow skipping forbidden namespaces.

## Configuration

The application requires the following environment variables for configuration:

- `OIDC_TOKEN_URL`: The URL of the OIDC provider's token endpoint.
- `OIDC_CLIENT_ID`: The client ID for the OIDC application.
- `OIDC_CLIENT_SECRET`: The client secret for the OIDC application (typically mounted from a Kubernetes Secret).
- `TARGET_NAMESPACES`: (Optional) Comma-separated list of specific Kubernetes namespaces to process (e.g., "default,kube-system,my-app-ns").
    - If set and non-empty, the application will only operate on these specified namespaces.
    - If empty or not set, the application will attempt to operate on all namespaces in the cluster.
- `OIDC_SCOPES`: (Optional) Space-separated scopes to request (e.g., "openid profile email"). Defaults to "openid".
- `K8S_SECRET_NAME`: The name of the Kubernetes Secret to be created in each target namespace (e.g., `oidc-token-secret`).
- `K8S_SECRET_KEY`: The key within the Kubernetes Secret where the token will be stored (e.g., `token`).

## Permissions

The required Kubernetes permissions depend on how `TARGET_NAMESPACES` is configured:

**Scenario 1: `TARGET_NAMESPACES` is NOT set or is empty (All Namespaces Mode)**

The ServiceAccount running the application needs:
- A `ClusterRole` with:
    - `list`, `get` on `namespaces` (cluster-wide).
    - `get`, `create`, `update`, `patch` on `secrets` (cluster-wide, though the application will iterate and RBAC would apply per namespace. Note: current app logic uses `log.Fatalf` on secret operation errors, including permission issues).
- A `ClusterRoleBinding` to bind this `ClusterRole` to the ServiceAccount.

**Scenario 2: `TARGET_NAMESPACES` IS set to specific namespaces**

The ServiceAccount running the application needs:
- For each namespace listed in `TARGET_NAMESPACES`:
    - A `Role` (namespaced) granting `get`, `create`, `update`, `patch` on `secrets` within that namespace.
    - A `RoleBinding` (namespaced) to bind this `Role` to the ServiceAccount within that namespace.
- In this mode, cluster-wide permission to `list` all `namespaces` is **not** required by the application.

## Development

To build the Go application:
```bash
go build -o oidc-jwt-fetcher main.go
```
