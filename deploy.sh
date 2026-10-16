#!/usr/bin/env bash
# Deploys the backend to Azure Container Apps (one replica: the PIN store lives in memory).
#
# Secrets come from your shell, never from a file:
#   export AI_API_KEY=... APP_API_KEY=...
#   export AI_BASE_URL=... AI_MODEL_A=... AI_MODEL_B=... ALLOWED_ORIGIN=https://...
#   ./deploy.sh
set -euo pipefail

RESOURCE_GROUP="rg-pillgaze"
LOCATION="westeurope"
APP_NAME="pillgaze-backend"
ENVIRONMENT="pillgaze-env"
MOCK_AI="${MOCK_AI:-false}"

: "${AI_API_KEY:?export AI_API_KEY}"
: "${APP_API_KEY:?export APP_API_KEY}"
: "${AI_BASE_URL:?export AI_BASE_URL}"
: "${AI_MODEL_A:?export AI_MODEL_A}"
: "${ALLOWED_ORIGIN:?export ALLOWED_ORIGIN}"
AI_MODEL_B="${AI_MODEL_B:-}"

cd "$(dirname "$0")"

az group create --name "$RESOURCE_GROUP" --location "$LOCATION" --output none

# Builds the Dockerfile in the cloud and creates or updates the app.
# On the very first run this revision has no secrets yet and fails its config
# check; the update below replaces it.
az containerapp up \
  --name "$APP_NAME" \
  --resource-group "$RESOURCE_GROUP" \
  --location "$LOCATION" \
  --environment "$ENVIRONMENT" \
  --source . \
  --ingress external \
  --target-port 8080

az containerapp secret set \
  --name "$APP_NAME" \
  --resource-group "$RESOURCE_GROUP" \
  --secrets "ai-api-key=$AI_API_KEY" "app-api-key=$APP_API_KEY" \
  --output none

az containerapp update \
  --name "$APP_NAME" \
  --resource-group "$RESOURCE_GROUP" \
  --min-replicas 1 \
  --max-replicas 1 \
  --set-env-vars \
    "AI_API_KEY=secretref:ai-api-key" \
    "APP_API_KEY=secretref:app-api-key" \
    "AI_BASE_URL=$AI_BASE_URL" \
    "AI_MODEL_A=$AI_MODEL_A" \
    "AI_MODEL_B=$AI_MODEL_B" \
    "ALLOWED_ORIGIN=$ALLOWED_ORIGIN" \
    "MOCK_AI=$MOCK_AI" \
  --output none

FQDN=$(az containerapp show --name "$APP_NAME" --resource-group "$RESOURCE_GROUP" \
  --query properties.configuration.ingress.fqdn --output tsv)
echo "Deployed: https://$FQDN/health"
