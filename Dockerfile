# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy vendored dependencies and source code
# No go mod download needed — vendor/ is committed
COPY . .

# Build static binary using vendored dependencies
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=vendor -o node-remediation ./cmd/manager

# Runtime stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /app/node-remediation /node-remediation

ENTRYPOINT ["/node-remediation"]
