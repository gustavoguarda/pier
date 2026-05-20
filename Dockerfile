# Build stage
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Copy go.mod/go.sum first to leverage layer cache when sources change.
COPY go.mod go.sum ./
RUN go mod download

# Then sources.
COPY cmd ./cmd
COPY internal ./internal

# Static, stripped binary so it runs on scratch/alpine without libc dependencies.
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w" \
    -o /out/saas-stub ./cmd/saas-stub

# Runtime stage
FROM alpine:3.22

RUN apk add --no-cache ca-certificates wget && \
    addgroup -S app && adduser -S -G app app

COPY --from=builder /out/saas-stub /usr/local/bin/saas-stub

USER app
EXPOSE 9000

HEALTHCHECK --interval=10s --timeout=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:${PORT:-9000}/healthz || exit 1

ENTRYPOINT ["/usr/local/bin/saas-stub"]
