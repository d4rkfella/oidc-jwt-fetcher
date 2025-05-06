# OIDC JWT Fetcher CronJob

This application runs as a Kubernetes CronJob. It fetches an OIDC JWT token using the client credentials grant and then creates/updates a Kubernetes Secret with this token in every namespace within the cluster.

## Functionality

1.  **Fetch OIDC Token**: Authenticates against an OIDC provider using client credentials to obtain a JWT.
2.  **List Namespaces**: Retrieves a list of all namespaces in the Kubernetes cluster.
3.  **Create/Update Secrets**: For each namespace, it creates (or updates if it already exists) a Kubernetes Secret containing the fetched JWT.

## Configuration

The application requires the following environment variables for configuration:

- `OIDC_TOKEN_URL`: The URL of the OIDC provider's token endpoint.
- `OIDC_CLIENT_ID`: The client ID for the OIDC application.
- `OIDC_CLIENT_SECRET`: The client secret for the OIDC application.
- `OIDC_SCOPES`: (Optional) Space-separated scopes to request (e.g., "openid profile email"). Defaults to "openid".
- `K8S_SECRET_NAME`: The name of the Kubernetes Secret to be created in each namespace (e.g., `oidc-token-secret`).
- `K8S_SECRET_KEY`: The key within the Kubernetes Secret where the token will be stored (e.g., `token`).
- `CRON_SCHEDULE`: The cron schedule for the job (e.g., `"0 * * * *"` for hourly). This is configured in the CronJob manifest.

## Permissions

The application needs the following Kubernetes permissions:

- `list` on `namespaces` (cluster-wide)
- `get`, `create`, `update`, `patch` on `secrets` (namespace-scoped, but applied to all namespaces via the logic)

## Development

To build the Go application:
```bash
go build -o oidc-jwt-fetcher main.go
```

To build the Docker image:
```bash
docker build -t your-repo/oidc-jwt-fetcher:latest .
``` 
