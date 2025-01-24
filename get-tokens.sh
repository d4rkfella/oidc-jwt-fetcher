#!/bin/bash

TOKEN=$(curl -X POST "$KEYCLOAK_URL" \
  -d "client_id=$CLIENT_ID" \
  -d "client_secret=$CLIENT_SECRET" \
  -d "grant_type=client_credentials" \
  -d "scope=$SCOPE" \
  -H "Content-Type: application/x-www-form-urlencoded" | jq -r .access_token)

kubectl create secret generic oidc-jwt \
  --from-literal=token="$TOKEN" \
  --dry-run=client -o yaml | kubectl apply -f -
