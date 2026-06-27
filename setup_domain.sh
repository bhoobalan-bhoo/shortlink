#!/usr/bin/env bash
#
# setup_domain.sh — Point go.bhoobalan.in at the deployed HTTP API.
#
# Does the whole chain (idempotent — safe to re-run):
#   1. find the bhoobalan.in Route53 hosted zone
#   2. request/reuse an ACM cert for go.bhoobalan.in (DNS-validated)
#   3. add the validation CNAME and wait until the cert is ISSUED
#   4. create the API Gateway custom domain (regional, TLS 1.2)
#   5. map it to the deployed HTTP API ($default stage)
#   6. upsert a Route53 alias A record  go.bhoobalan.in -> the API domain
#
# Prereq: ./deploy.sh has been run (the HTTP API must exist).
# Run:    ./setup_domain.sh
#
set -euo pipefail

export AWS_PROFILE=bhoo
REGION="${AWS_REGION:-ap-south-1}"
APEX="bhoobalan.in"
DOMAIN="go.bhoobalan.in"

echo "Profile : $AWS_PROFILE"
echo "Region  : $REGION"
echo "Domain  : $DOMAIN"
echo

# --- 1. hosted zone -------------------------------------------------------
ZONE_ID=$(aws route53 list-hosted-zones-by-name --dns-name "$APEX" \
  --query "HostedZones[?Name=='${APEX}.'].Id | [0]" --output text)
if [ "$ZONE_ID" = "None" ] || [ -z "$ZONE_ID" ]; then
  echo "ERROR: no Route53 hosted zone found for $APEX"; exit 1
fi
ZONE_ID=${ZONE_ID##*/}
echo "Hosted zone: $ZONE_ID"

# --- 2. ACM certificate ---------------------------------------------------
CERT_ARN=$(aws acm list-certificates --region "$REGION" \
  --query "CertificateSummaryList[?DomainName=='${DOMAIN}'].CertificateArn | [0]" --output text)
if [ "$CERT_ARN" = "None" ] || [ -z "$CERT_ARN" ]; then
  echo "Requesting ACM certificate for $DOMAIN ..."
  CERT_ARN=$(aws acm request-certificate --region "$REGION" \
    --domain-name "$DOMAIN" --validation-method DNS \
    --query CertificateArn --output text)
fi
echo "Certificate: $CERT_ARN"

STATUS=$(aws acm describe-certificate --region "$REGION" --certificate-arn "$CERT_ARN" \
  --query "Certificate.Status" --output text)

if [ "$STATUS" != "ISSUED" ]; then
  # --- 3. add DNS validation record + wait --------------------------------
  echo "Fetching DNS validation record ..."
  for i in $(seq 1 15); do
    VAL_NAME=$(aws acm describe-certificate --region "$REGION" --certificate-arn "$CERT_ARN" \
      --query "Certificate.DomainValidationOptions[0].ResourceRecord.Name" --output text)
    VAL_VALUE=$(aws acm describe-certificate --region "$REGION" --certificate-arn "$CERT_ARN" \
      --query "Certificate.DomainValidationOptions[0].ResourceRecord.Value" --output text)
    [ "$VAL_NAME" != "None" ] && [ -n "$VAL_NAME" ] && break
    sleep 2
  done

  echo "Adding validation CNAME to Route53 ..."
  aws route53 change-resource-record-sets --hosted-zone-id "$ZONE_ID" --change-batch "{
    \"Changes\": [{
      \"Action\": \"UPSERT\",
      \"ResourceRecordSet\": {
        \"Name\": \"${VAL_NAME}\", \"Type\": \"CNAME\", \"TTL\": 300,
        \"ResourceRecords\": [{ \"Value\": \"${VAL_VALUE}\" }]
      }
    }]
  }" >/dev/null

  echo "Waiting for certificate to be ISSUED (can take a few minutes) ..."
  aws acm wait certificate-validated --region "$REGION" --certificate-arn "$CERT_ARN"
fi
echo "Certificate is ISSUED."

# --- 4. API Gateway custom domain ----------------------------------------
if ! aws apigatewayv2 get-domain-name --domain-name "$DOMAIN" --region "$REGION" >/dev/null 2>&1; then
  echo "Creating API Gateway custom domain ..."
  aws apigatewayv2 create-domain-name --region "$REGION" --domain-name "$DOMAIN" \
    --domain-name-configurations "CertificateArn=${CERT_ARN},EndpointType=REGIONAL,SecurityPolicy=TLS_1_2" >/dev/null
fi
TARGET=$(aws apigatewayv2 get-domain-name --domain-name "$DOMAIN" --region "$REGION" \
  --query "DomainNameConfigurations[0].ApiGatewayDomainName" --output text)
TARGET_ZONE=$(aws apigatewayv2 get-domain-name --domain-name "$DOMAIN" --region "$REGION" \
  --query "DomainNameConfigurations[0].HostedZoneId" --output text)
echo "API domain target: $TARGET"

# --- 5. map to the deployed HTTP API -------------------------------------
API_ID="${API_ID:-$(aws apigatewayv2 get-apis --region "$REGION" \
  --query "Items[?contains(Name, 'bhoo-shortlink')].ApiId | [0]" --output text)}"
if [ "$API_ID" = "None" ] || [ -z "$API_ID" ]; then
  echo "ERROR: could not find the deployed HTTP API. Run ./deploy.sh first."; exit 1
fi
echo "HTTP API: $API_ID"

MAPPING=$(aws apigatewayv2 get-api-mappings --domain-name "$DOMAIN" --region "$REGION" \
  --query "Items[?ApiId=='${API_ID}'].ApiMappingId | [0]" --output text)
if [ "$MAPPING" = "None" ] || [ -z "$MAPPING" ]; then
  echo "Creating API mapping ..."
  aws apigatewayv2 create-api-mapping --region "$REGION" \
    --domain-name "$DOMAIN" --api-id "$API_ID" --stage '$default' >/dev/null
fi

# --- 6. Route53 alias record ---------------------------------------------
echo "Upserting Route53 alias $DOMAIN -> $TARGET ..."
aws route53 change-resource-record-sets --hosted-zone-id "$ZONE_ID" --change-batch "{
  \"Changes\": [{
    \"Action\": \"UPSERT\",
    \"ResourceRecordSet\": {
      \"Name\": \"${DOMAIN}\", \"Type\": \"A\",
      \"AliasTarget\": {
        \"DNSName\": \"${TARGET}\",
        \"HostedZoneId\": \"${TARGET_ZONE}\",
        \"EvaluateTargetHealth\": false
      }
    }
  }]
}" >/dev/null

echo
echo "Done. https://${DOMAIN} now routes to the Lambda (DNS may take a minute)."
