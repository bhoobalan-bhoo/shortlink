# --- build ---
FROM golang:1.26 AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=docker
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X main.version=${VERSION}" -o /bhoos ./cmd/local

# --- run ---
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /bhoos /bhoos
EXPOSE 8080
ENV ADDR=:8080
ENTRYPOINT ["/bhoos"]
