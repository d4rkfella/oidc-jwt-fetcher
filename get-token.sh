#!/bin/bash

TOKEN=$(curl -X POST "$KEYCLOAK_URL" \
  -d "client_id=$CLIENT_ID" \
  -d "client_secret=$CLIENT_SECRET" \
  -d "grant_type=client_credentials" \
  -d "scope=$SCOPE" \
  -H "Content-Type: application/x-www-form-urlencoded" | jq -r .access_token)

echo -e "apiVersion: v1
kind: Secret
metadata:
  name: oidc-jwt
stringData:
  headers: |
    Authorization: Bearer $TOKEN" | kubectl apply -f -
