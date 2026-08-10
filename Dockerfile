# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Copy go mod files first for better layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o cmk-node-remediation ./cmd/manager

# Runtime stage
FROM gcr.io/distroless/static-debian12

COPY --from=builder /app/cmk-node-remediation /cmk-node-remediation

ENTRYPOINT ["/cmk-node-remediation"]
