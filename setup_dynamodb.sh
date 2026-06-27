#!/usr/bin/env bash
#
# setup_dynamodb.sh — Creates the DynamoDB tables for the URL shortener.
#
# Tables
#   bhoo-urls    link metadata
#     - PK: slug (S)           short code, e.g. "abc123" or a custom path
#     - GSI: urlHash-index     PK urlHash (S) -> dedupe identical long URLs
#     - TTL: expiresAt (N)     auto-delete expiring links
#
#   bhoo-clicks  one item per click (for /track-urls logs)
#     - PK: slug (S)           which link was clicked
#     - SK: sk (S)             "<epochMillis>#<rand>"  -> sortable, newest last
#       attributes: ip, city, region, country, lat, lon, resolved, ua, ts
#
#   Billing: PAY_PER_REQUEST (on-demand) for both.
#
# Run manually:  ./setup_dynamodb.sh
#
set -euo pipefail

AWS_PROFILE="bhoo"
AWS_REGION="${AWS_REGION:-ap-south-1}"   # override: AWS_REGION=us-east-1 ./setup_dynamodb.sh
URLS_TABLE="bhoo-urls"
CLICKS_TABLE="bhoo-clicks"

echo "Profile : $AWS_PROFILE"
echo "Region  : $AWS_REGION"
echo

table_exists() {
  aws dynamodb describe-table \
    --table-name "$1" \
    --profile "$AWS_PROFILE" \
    --region "$AWS_REGION" >/dev/null 2>&1
}

# --- bhoo-urls -------------------------------------------------------------
create_urls_table() {
  if table_exists "$URLS_TABLE"; then
    echo "Table '$URLS_TABLE' already exists. Skipping."
    return
  fi
  echo "Creating table '$URLS_TABLE' ..."
  aws dynamodb create-table \
    --table-name "$URLS_TABLE" \
    --billing-mode PAY_PER_REQUEST \
    --attribute-definitions \
        AttributeName=slug,AttributeType=S \
        AttributeName=urlHash,AttributeType=S \
    --key-schema \
        AttributeName=slug,KeyType=HASH \
    --global-secondary-indexes '[
      {
        "IndexName": "urlHash-index",
        "KeySchema": [ { "AttributeName": "urlHash", "KeyType": "HASH" } ],
        "Projection": {
          "ProjectionType": "INCLUDE",
          "NonKeyAttributes": ["slug", "longUrl"]
        }
      }
    ]' \
    --profile "$AWS_PROFILE" --region "$AWS_REGION" >/dev/null

  aws dynamodb wait table-exists \
    --table-name "$URLS_TABLE" --profile "$AWS_PROFILE" --region "$AWS_REGION"

  echo "Enabling TTL on '$URLS_TABLE.expiresAt' ..."
  aws dynamodb update-time-to-live \
    --table-name "$URLS_TABLE" \
    --time-to-live-specification "Enabled=true,AttributeName=expiresAt" \
    --profile "$AWS_PROFILE" --region "$AWS_REGION" >/dev/null
  echo "  '$URLS_TABLE' ready."
}

# --- bhoo-clicks -----------------------------------------------------------
create_clicks_table() {
  if table_exists "$CLICKS_TABLE"; then
    echo "Table '$CLICKS_TABLE' already exists. Skipping."
    return
  fi
  echo "Creating table '$CLICKS_TABLE' ..."
  aws dynamodb create-table \
    --table-name "$CLICKS_TABLE" \
    --billing-mode PAY_PER_REQUEST \
    --attribute-definitions \
        AttributeName=slug,AttributeType=S \
        AttributeName=sk,AttributeType=S \
    --key-schema \
        AttributeName=slug,KeyType=HASH \
        AttributeName=sk,KeyType=RANGE \
    --profile "$AWS_PROFILE" --region "$AWS_REGION" >/dev/null

  aws dynamodb wait table-exists \
    --table-name "$CLICKS_TABLE" --profile "$AWS_PROFILE" --region "$AWS_REGION"
  echo "  '$CLICKS_TABLE' ready."
}

create_urls_table
create_clicks_table

echo
echo "Done. Tables are ACTIVE:"
echo "  - $URLS_TABLE   (links + urlHash-index GSI + TTL)"
echo "  - $CLICKS_TABLE (click logs with IP + geo)"
