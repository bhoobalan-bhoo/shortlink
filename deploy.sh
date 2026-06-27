#!/usr/bin/env bash
#
# deploy.sh — Build the Go binary and deploy bhoo-shortlink to AWS Lambda.
#
# Prereqs (one time):
#   - ./setup_dynamodb.sh has been run (creates bhoo-urls + bhoo-clicks)
#   - serverless framework v3 installed (already on this machine)
#   - AWS profile "bhoo" configured
#
# Optional env:
#   BASE_URL     public URL used in rendered links (default https://go.bhoobalan.in)
#   ADMIN_TOKEN  if set, /track-urls requires ?token=… (recommended for public)
#
# Run:  ./deploy.sh
#
set -euo pipefail

export AWS_PROFILE=bhoo
REGION="${AWS_REGION:-ap-south-1}"
export BASE_URL="${BASE_URL:-https://go.bhoobalan.in}"
export ADMIN_TOKEN="${ADMIN_TOKEN:-}"

echo "Profile  : $AWS_PROFILE"
echo "Region   : $REGION"
echo "Base URL : $BASE_URL"
[ -z "$ADMIN_TOKEN" ] && echo "WARNING  : ADMIN_TOKEN is empty — /track-urls will be PUBLIC."
echo

# 1. Build a static ARM64 Linux binary named 'bootstrap' (required by the
#    provided.al2023 custom runtime). Templates + CSS are embedded in it.
echo "Building bootstrap (linux/arm64)..."
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
  go build -tags lambda.norpc -ldflags="-s -w" -o bootstrap ./cmd/lambda

# 2. Deploy with Serverless Framework.
echo "Deploying..."
serverless deploy --region "$REGION" --verbose

# 3. Clean up the local artifact.
rm -f bootstrap

echo
echo "Deployed. The HTTP API URL is shown above as 'endpoint:'."
echo "Next: point go.bhoobalan.in at it with ./setup_domain.sh"
