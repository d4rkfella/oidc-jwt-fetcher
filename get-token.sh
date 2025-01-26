#!/bin/bash

if [[ -z "$KEYCLOAK_URL" ]]; then
  echo "Error: KEYCLOAK_URL is not set."
  exit 1
fi

if [[ -z "$CLIENT_ID" ]]; then
  echo "Error: CLIENT_ID is not set."
  exit 1
fi

if [[ ! -f "$CLIENT_SECRET_FILE" ]]; then
  echo "Error: CLIENT_SECRET_FILE does not exist or is not accessible at $CLIENT_SECRET_FILE."
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
  echo "Error: Failed to retrieve the OIDC token. Please check the provided credentials."
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
