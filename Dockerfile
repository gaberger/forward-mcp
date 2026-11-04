# syntax=docker/dockerfile:1
# Build stage. CGO is required by github.com/mattn/go-sqlite3.
FROM golang:1.25-alpine AS builder

RUN apk add --no-cache build-base sqlite-dev

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/forward-mcp ./cmd/server

# Runtime stage.
FROM alpine:3.20

RUN apk add --no-cache ca-certificates sqlite-libs \
    && addgroup -S forward && adduser -S -G forward -h /app forward

WORKDIR /app
COPY --from=builder /out/forward-mcp /app/forward-mcp
# Certified NQE library and spec fallbacks ship with the image.
COPY --chown=forward:forward spec/ /app/spec/

# Runtime state (SQLite databases, bloom indexes, disk cache).
RUN mkdir -p /app/.forward-mcp/data /app/data && chown -R forward:forward /app
ENV HOME=/app \
    FORWARD_CERTIFIED_LIBRARY_PATH=/app/spec/certified \
    SERVER_HOST=0.0.0.0 \
    SERVER_PORT=8080

USER forward
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=10s \
    CMD wget -qO- http://127.0.0.1:8080/healthz >/dev/null 2>&1 || exit 1

# Remote mode by default; override CMD with no args for stdio.
ENTRYPOINT ["/app/forward-mcp"]
CMD ["serve-http"]
