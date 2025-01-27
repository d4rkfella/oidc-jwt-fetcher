#!/bin/bash

if [[ -z "${$KEYCLOAK_URL}"         ||
      -z "${CLIENT_ID}"             ||
]]; then
    printf "\e[1;32m%-6s\e[m\n" "Invalid configuration - missing a required environment variable"
    [[ -z "${KEYCLOAK_URL}" ]]             && printf "\e[1;32m%-6s\e[m\n" "KEYCLOAK_URL: unset"
    [[ -z "${CLIENT_ID}" ]]                && printf "\e[1;32m%-6s\e[m\n" "CLIENT_ID: unset"
    exit 1
fi

if [[ ! -f "$CLIENT_SECRET_FILE" ]]; then
  printf "\e[1;32m%-6s\e[m\n" "CLIENT_SECRET_FILE does not exist or is not accessible at $CLIENT_SECRET_FILE"
  exit 1
fi

if [[ -z "$SCOPE" ]]; then
  TOKEN=$(curl -sS -X POST "$KEYCLOAK_URL" \
    -d "client_id=$CLIENT_ID" \
    -d "client_secret=$(cat "$CLIENT_SECRET_FILE")" \
    -d "grant_type=client_credentials" \
    -H "Content-Type: application/x-www-form-urlencoded" | jq -r .access_token)
else
  TOKEN=$(curl -sS -X POST "$KEYCLOAK_URL" \
    -d "client_id=$CLIENT_ID" \
    -d "client_secret=$(cat "$CLIENT_SECRET_FILE")" \
    -d "grant_type=client_credentials" \
    -d "scope=$SCOPE" \
    -H "Content-Type: application/x-www-form-urlencoded" | jq -r .access_token)
fi

if [[ -z "$TOKEN" ]]; then
  printf "\e[1;32m%-6s\e[m\n" "Failed to retrieve the OIDC token. Please check the provided credentials."
  exit 1
fi

kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: oidc-jwt
type: Opaque
stringData:
  token: $TOKEN
EOF
