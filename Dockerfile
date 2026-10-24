# -----------------------------------------------------------------------------
# Stage 1: Build binary
# -----------------------------------------------------------------------------
FROM golang:alpine AS builder

WORKDIR /app

# Copy application source code and web assets
COPY . .

# Compile static binary with optimizations (-s -w strip debug info)
# Builds only dependencies required for the server binary (excludes testcontainers)
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /app/bin/server .

# -----------------------------------------------------------------------------
# Stage 2: Minimal Production Image
# -----------------------------------------------------------------------------
FROM alpine:3.21

# Create non-root user for security best practices
RUN addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

# Copy root CA certificates from builder stage for secure TLS connections
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

# Copy binary and runtime web assets (templates & static files)
COPY --from=builder /app/bin/server /app/server
COPY --from=builder /app/web /app/web

# Set ownership to unprivileged user
RUN chown -R appuser:appgroup /app

USER appuser

EXPOSE 8080

ENTRYPOINT ["/app/server"]
