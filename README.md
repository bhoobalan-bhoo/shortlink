# 🌿 bhoo-shortlink

A calm, self-hostable **URL shortener** written in Go — with custom paths, QR
codes, expiring links, and a click-tracking dashboard that plots visitors on a
3D globe. Built to run cheaply on **AWS Lambda + DynamoDB**, and just as happily
as a single binary or Docker container anywhere.

Live: **https://go.bhoobalan.in**

[![CI](https://github.com/bhoobalan-bhoo/shortlink/actions/workflows/ci.yml/badge.svg)](https://github.com/bhoobalan-bhoo/shortlink/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/bhoobalan-bhoo/shortlink?sort=semver)](https://github.com/bhoobalan-bhoo/shortlink/releases)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.26-00ADD8.svg)](https://go.dev)

---

## Features

- **Short links** with random base62 codes, or your own **custom path**
- **Expiring links** (1 hour → 30 days) via DynamoDB TTL — auto-cleaned, free
- **QR code** for every link
- **Click tracking** — captures IP, approximate city/country (via ip-api.com),
  and device, logged per click
- **Tracked-URLs dashboard** — a Three.js globe that flies to each click
  location, with per-link stats (total clicks, top location, last click)
- **Duplicate-safe** — shortening the same URL twice reuses the existing code
- **One handler, two runtimes** — the same `http.Handler` serves locally and on
  Lambda, so there's no code drift between dev and prod
- **Token-gated dashboard** — set `ADMIN_TOKEN` to keep visitor logs private

## Tech stack

| Layer    | Choice |
|----------|--------|
| Language | Go 1.26 (stdlib `net/http` routing, `html/template`, `embed`) |
| Storage  | DynamoDB (on-demand) — `bhoo-urls` + `bhoo-clicks` |
| Frontend | Server-rendered templates + HTMX, Three.js globe (no build step) |
| Hosting  | AWS Lambda (`provided.al2023`, arm64) behind an HTTP API |
| Deploy   | Serverless Framework v3 |

## Quick start (local)

Requires Go 1.26+ and AWS credentials with DynamoDB access.

```bash
# 1. create the DynamoDB tables (uses AWS profile "bhoo" by default)
./setup_dynamodb.sh

# 2. run the server
make run            # or: go run ./cmd/local
# → http://localhost:8080
```

### Configuration (env vars)

| Var            | Default                  | Notes |
|----------------|--------------------------|-------|
| `AWS_PROFILE`  | `bhoo`                   | local only; Lambda uses its role |
| `AWS_REGION`   | `ap-south-1`             | |
| `TABLE_NAME`   | `bhoo-urls`              | links table |
| `CLICKS_TABLE` | `bhoo-clicks`            | click-log table |
| `BASE_URL`     | `http://localhost:8080`  | origin used in rendered links |
| `ADDR`         | `:8080`                  | local listen address |
| `ADMIN_TOKEN`  | _(empty)_                | if set, `/track-urls` needs `?token=…` |

## Deploy to AWS

```bash
# build + deploy the Lambda (set a token to protect the dashboard)
ADMIN_TOKEN='your-secret' ./deploy.sh

# point go.bhoobalan.in at the HTTP API (ACM cert + Route53 + custom domain)
./setup_domain.sh
```

`deploy.sh` cross-compiles a static ARM64 `bootstrap`, packages just that
binary (templates and CSS are embedded), and runs `serverless deploy` with the
`bhoo` profile. IAM is scoped to exactly the two tables.

## Run with Docker

```bash
docker run --rm -p 8080:8080 \
  -e AWS_ACCESS_KEY_ID=… -e AWS_SECRET_ACCESS_KEY=… -e AWS_REGION=ap-south-1 \
  ghcr.io/bhoobalan-bhoo/shortlink:latest
```

## Project layout

```
cmd/local/        local HTTP server (dev / self-host)
cmd/lambda/       AWS Lambda entrypoint (same handler)
internal/shortid/ random base62 slug generator
internal/store/   DynamoDB persistence (links + click logs)
internal/geo/     IP → city/country lookup
internal/handler/ routes, embedded templates + static CSS
setup_dynamodb.sh provision DynamoDB tables
deploy.sh         build + serverless deploy
setup_domain.sh   ACM + Route53 custom domain
```

## Routes

| Method | Path                        | Purpose |
|--------|-----------------------------|---------|
| GET    | `/`                         | shorten form |
| POST   | `/shorten`                  | create a link (HTMX) |
| GET    | `/{slug}`                   | redirect + log the click |
| GET    | `/{slug}/qr`                | PNG QR code |
| GET    | `/{slug}/stats`             | per-link stats |
| GET    | `/track-urls`               | dashboard (token-gated) |
| GET    | `/track-urls/{slug}/logs`   | click data for the globe |

## Privacy

This app stores visitor IP addresses and approximate locations to power the
click dashboard. If you run it publicly, set `ADMIN_TOKEN` so the logs aren't
exposed, and tell your users what you collect.

## Development

```bash
make fmt vet test    # format, vet, test
make build           # build ./cmd/local into ./bin/bhoos
make build-lambda    # cross-compile the Lambda bootstrap
```

Contributions welcome — open an issue or PR.

## License

[MIT](LICENSE) © Bhoobalan B R
