// Command local runs the URL shortener as a plain HTTP server for development.
package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"

	"github.com/bhoobalan-bhoo/shortlink/internal/handler"
	"github.com/bhoobalan-bhoo/shortlink/internal/store"
)

// version is overridden at release time via -ldflags "-X main.version=…".
var version = "dev"

func main() {
	ctx := context.Background()

	region := getenv("AWS_REGION", "ap-south-1")
	profile := getenv("AWS_PROFILE", "bhoo")
	table := getenv("TABLE_NAME", "bhoo-urls")
	clicksTable := getenv("CLICKS_TABLE", "bhoo-clicks")
	addr := getenv("ADDR", ":8080")
	baseURL := getenv("BASE_URL", "http://localhost:8080")
	adminToken := os.Getenv("ADMIN_TOKEN") // empty = dashboard open (dev)

	cfg, err := config.LoadDefaultConfig(ctx,
		config.WithRegion(region),
		config.WithSharedConfigProfile(profile),
	)
	if err != nil {
		log.Fatalf("load aws config: %v", err)
	}

	st := store.New(dynamodb.NewFromConfig(cfg), table, clicksTable)
	h, err := handler.New(st, baseURL, adminToken)
	if err != nil {
		log.Fatalf("init handler: %v", err)
	}

	log.Printf("bhoo-shortlink %s — listening on %s  (table=%s region=%s profile=%s)", version, addr, table, region, profile)
	if err := http.ListenAndServe(addr, h.Routes()); err != nil {
		log.Fatal(err)
	}
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
