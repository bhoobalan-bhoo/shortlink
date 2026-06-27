# bhoo-shortlink — common tasks
.DEFAULT_GOAL := help

BINARY := bhoos
LAMBDA_GOOS := linux
LAMBDA_GOARCH := arm64

.PHONY: help run build build-lambda deploy domain tables docker tidy fmt vet test clean

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

run: ## Run the local server (http://localhost:8080)
	AWS_PROFILE=bhoo go run ./cmd/local

build: ## Build the local server into ./bin/$(BINARY)
	go build -ldflags="-s -w" -o bin/$(BINARY) ./cmd/local

build-lambda: ## Cross-compile the Lambda bootstrap (linux/arm64)
	GOOS=$(LAMBDA_GOOS) GOARCH=$(LAMBDA_GOARCH) CGO_ENABLED=0 \
		go build -tags lambda.norpc -ldflags="-s -w" -o bootstrap ./cmd/lambda

deploy: ## Build + deploy to AWS Lambda (profile bhoo)
	./deploy.sh

domain: ## Configure the go.bhoobalan.in custom domain
	./setup_domain.sh

tables: ## Create the DynamoDB tables
	./setup_dynamodb.sh

docker: ## Build a local Docker image
	docker build -t $(BINARY):local .

tidy: ## go mod tidy
	go mod tidy

fmt: ## Format the code
	go fmt ./...

vet: ## Run go vet
	go vet ./...

test: ## Run tests
	go test ./...

clean: ## Remove build artifacts
	rm -rf bin bootstrap dist .serverless
