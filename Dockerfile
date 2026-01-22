# Build stage - compile Go binary
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install ca-certificates for HTTPS and git for go mod
RUN apk add --no-cache ca-certificates git

# Copy dependency files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary with optimizations
# CGO_ENABLED=0 for static linking (no libc dependency)
# -ldflags="-s -w" strips debug info and symbol table
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w" \
    -o /proxy \
    ./cmd/proxy

# Runtime stage - minimal image
# distroless has no shell, no package manager - minimal attack surface
FROM gcr.io/distroless/static-debian12:nonroot

# Copy binary from builder
COPY --from=builder /proxy /proxy

# Cloud Run expects port 8080 by default
EXPOSE 8080

# Run as non-root user (distroless:nonroot = UID 65532)
USER nonroot:nonroot

# Health check endpoint for Cloud Run
# Note: distroless has no shell, so no HEALTHCHECK instruction
# Cloud Run handles health checks via HTTP

ENTRYPOINT ["/proxy"]
