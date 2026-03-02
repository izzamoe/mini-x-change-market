# Stage 1: Build
FROM golang:1.26-alpine AS builder

RUN apk --no-cache add ca-certificates tzdata git

WORKDIR /app

# Download dependencies first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source and build
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /server ./cmd/server

# Stage 2: Minimal runtime image
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata wget

COPY --from=builder /server /server

EXPOSE 8080

ENTRYPOINT ["/server"]
