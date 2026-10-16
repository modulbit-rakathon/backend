#!/usr/bin/env bash
# Deploys the backend to Azure App Service for Containers, one instance
# (the PIN store lives in memory). Container Apps would be simpler, but
# Microsoft.App is not registered in the hackathon subscription.
#
# Secrets come from your shell, never from a file in the repo:
#   set -a; . ./.env; set +a
#   ./deploy.sh
set -euo pipefail

RESOURCE_GROUP="rg-team92"   # the only group our account may write to
LOCATION="swedencentral"
ACR_NAME="pillgazeteam92"    # globally unique, letters and digits only
PLAN_NAME="pillgaze-plan"
APP_NAME="pillgaze-team92"   # -> https://pillgaze-team92.azurewebsites.net
SKU="B1"
MOCK_AI="${MOCK_AI:-false}"

: "${AI_API_KEY:?export AI_API_KEY}"
: "${APP_API_KEY:?export APP_API_KEY}"
: "${AI_BASE_URL:?export AI_BASE_URL}"
: "${AI_MODEL_A:?export AI_MODEL_A}"
: "${ALLOWED_ORIGIN:?export ALLOWED_ORIGIN}"
AI_MODEL_B="${AI_MODEL_B:-}"

cd "$(dirname "$0")"
TAG="$(git rev-parse --short HEAD)$(git diff --quiet || echo -dirty)"
IMAGE="$ACR_NAME.azurecr.io/pillgaze-backend:$TAG"

if ! az acr show --name "$ACR_NAME" --resource-group "$RESOURCE_GROUP" --output none 2>/dev/null; then
  # Admin user: our role cannot grant AcrPull to a managed identity.
  az acr create --name "$ACR_NAME" --resource-group "$RESOURCE_GROUP" --location "$LOCATION" \
    --sku Basic --admin-enabled true --output none
fi

# Build in the cloud from the Dockerfile.
az acr build --registry "$ACR_NAME" --image "pillgaze-backend:$TAG" . --output none

if ! az appservice plan show --name "$PLAN_NAME" --resource-group "$RESOURCE_GROUP" --output none 2>/dev/null; then
  az appservice plan create --name "$PLAN_NAME" --resource-group "$RESOURCE_GROUP" --location "$LOCATION" \
    --is-linux --sku "$SKU" --number-of-workers 1 --output none
fi

ACR_USER=$(az acr credential show --name "$ACR_NAME" --query username --output tsv)
ACR_PASS=$(az acr credential show --name "$ACR_NAME" --query "passwords[0].value" --output tsv)

if ! az webapp show --name "$APP_NAME" --resource-group "$RESOURCE_GROUP" --output none 2>/dev/null; then
  az webapp create --name "$APP_NAME" --resource-group "$RESOURCE_GROUP" --plan "$PLAN_NAME" \
    --container-image-name "$IMAGE" \
    --container-registry-url "https://$ACR_NAME.azurecr.io" \
    --container-registry-user "$ACR_USER" \
    --container-registry-password "$ACR_PASS" \
    --https-only true --output none
fi

# App settings are encrypted at rest and reach the container as env vars.
az webapp config appsettings set --name "$APP_NAME" --resource-group "$RESOURCE_GROUP" --output none --settings \
  "WEBSITES_PORT=8080" \
  "PORT=8080" \
  "AI_API_KEY=$AI_API_KEY" \
  "APP_API_KEY=$APP_API_KEY" \
  "AI_BASE_URL=$AI_BASE_URL" \
  "AI_MODEL_A=$AI_MODEL_A" \
  "AI_MODEL_B=$AI_MODEL_B" \
  "ALLOWED_ORIGIN=$ALLOWED_ORIGIN" \
  "MOCK_AI=$MOCK_AI"

# Always on: an idle unload would wipe the in-memory PIN store.
az webapp config set --name "$APP_NAME" --resource-group "$RESOURCE_GROUP" \
  --always-on true --http20-enabled true --output none

az webapp config container set --name "$APP_NAME" --resource-group "$RESOURCE_GROUP" \
  --container-image-name "$IMAGE" \
  --container-registry-url "https://$ACR_NAME.azurecr.io" \
  --container-registry-user "$ACR_USER" \
  --container-registry-password "$ACR_PASS" \
  --output none

az webapp restart --name "$APP_NAME" --resource-group "$RESOURCE_GROUP" --output none

HOST=$(az webapp show --name "$APP_NAME" --resource-group "$RESOURCE_GROUP" --query defaultHostName --output tsv)
echo "Deployed $IMAGE"
echo "https://$HOST/health"
