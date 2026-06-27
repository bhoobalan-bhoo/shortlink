// Command lambda runs the URL shortener on AWS Lambda behind an HTTP API.
// It reuses the exact same http.Handler as the local server.
package main

import (
	"context"
	"log"
	"os"

	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/awslabs/aws-lambda-go-api-proxy/httpadapter"

	"github.com/bhoobalan-bhoo/shortlink/internal/handler"
	"github.com/bhoobalan-bhoo/shortlink/internal/store"
)

func main() {
	ctx := context.Background()

	// On Lambda, AWS_REGION is injected by the runtime and credentials come
	// from the execution role — no shared profile.
	table := getenv("TABLE_NAME", "bhoo-urls")
	clicksTable := getenv("CLICKS_TABLE", "bhoo-clicks")
	baseURL := getenv("BASE_URL", "https://go.bhoobalan.in")
	adminToken := os.Getenv("ADMIN_TOKEN")

	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		log.Fatalf("load aws config: %v", err)
	}

	st := store.New(dynamodb.NewFromConfig(cfg), table, clicksTable)
	h, err := handler.New(st, baseURL, adminToken)
	if err != nil {
		log.Fatalf("init handler: %v", err)
	}

	// HTTP API uses payload format v2.
	lambda.Start(httpadapter.NewV2(h.Routes()).ProxyWithContext)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
