#!/bin/bash

# Get the OIDC token
TOKEN=$(curl -X POST "$KEYCLOAK_URL" \
  -d "client_id=$CLIENT_ID" \
  -d "client_secret=$CLIENT_SECRET" \
  -d "grant_type=client_credentials" \
  -d "scope=$SCOPE" \
  -H "Content-Type: application/x-www-form-urlencoded" | jq -r .access_token)

kubectl apply -f - <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: oidc-jwt
type: Opaque
stringData:
  token: $TOKEN
EOF
