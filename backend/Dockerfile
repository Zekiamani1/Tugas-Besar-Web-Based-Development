# -----------------------------------------------------------------------------
# Stage 1: Download Go modules & build server and worker binaries
# -----------------------------------------------------------------------------
FROM golang:alpine AS deps

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

FROM deps AS build-server
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/server ./cmd/server

FROM deps AS build-worker
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/worker ./cmd/worker

# -----------------------------------------------------------------------------
# Stage 2: Shared minimal runtime base
# -----------------------------------------------------------------------------
FROM alpine:3.21 AS runtime-base

RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

COPY --from=deps /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# -----------------------------------------------------------------------------
# Stage 3: Standalone worker runtime (--target worker)
# -----------------------------------------------------------------------------
FROM runtime-base AS worker

COPY --from=build-worker --chown=appuser:appgroup /app/bin/worker /app/worker

USER appuser

ENTRYPOINT ["/app/worker"]

# -----------------------------------------------------------------------------
# Stage 4: HTTP API server runtime (--target server, default)
# -----------------------------------------------------------------------------
FROM runtime-base AS server

COPY --from=build-server --chown=appuser:appgroup /app/bin/server /app/server

USER appuser

EXPOSE 8080

ENTRYPOINT ["/app/server"]
