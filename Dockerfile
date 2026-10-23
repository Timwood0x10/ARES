# syntax=docker/dockerfile:1

# The image this repository publishes is the SERVER (`ares serve`), not an example binary:
# the CD workflow tags it ghcr.io/<repo>:latest, and a demo entrypoint under that name would
# promise a service that is not in the image.

FROM golang:1.26-alpine AS builder

WORKDIR /src

# git is needed by `go mod download` for any module fetched over a VCS URL; ca-certificates so
# the builder can reach a proxy over TLS.
RUN apk add --no-cache git ca-certificates

# Dependencies first: this layer is reused until go.mod/go.sum change, which is what keeps a
# code-only rebuild from re-downloading the module cache.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off so the result runs on a bare runtime image (no libc to match). -trimpath and -s -w keep
# the binary reproducible and smaller; VERSION is baked in so `ares version` inside the container
# reports the tag the image was built from rather than a stale constant.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/ares ./cmd/ares

FROM alpine:3.20

# ca-certificates for outbound TLS (LLM providers, MCP servers); tzdata so a UTC schedule is not
# silently shifted by a missing zone database.
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S ares \
    && adduser -S -G ares -h /app ares

WORKDIR /app
COPY --from=builder /out/ares /usr/local/bin/ares

# The config is MOUNTED, never baked: ares.yaml carries the API key, provider endpoints and any
# tenant/JWT secret, and an image layer is the one place a secret cannot be taken back from.
# `serve` fails fast with "load config" when the file is absent, which is the behaviour an
# operator wants — a container that starts without its config would serve defaults nobody chose.
VOLUME ["/app"]
EXPOSE 8080

USER ares
ENTRYPOINT ["/usr/local/bin/ares"]
CMD ["serve", "--config", "/app/ares.yaml"]
