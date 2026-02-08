# ── Stage 1: Build ────────────────────────────────────────────────
FROM golang:1.24-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /bin/popcornvault ./cmd/popcornvault

# ── Stage 2: Runtime ──────────────────────────────────────────────
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /bin/popcornvault /app/popcornvault
COPY --from=builder /src/migrations   /app/migrations

EXPOSE 8080

ENTRYPOINT ["/app/popcornvault"]
