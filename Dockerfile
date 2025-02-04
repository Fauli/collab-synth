# syntax=docker/dockerfile:1

# ---------------------------
# Build Stage
# ---------------------------
FROM golang:1.20-alpine AS builder

# Install git (if required by your go.mod dependencies)
RUN apk update && apk add --no-cache git

# Set working directory
WORKDIR /app

# Copy go.mod and go.sum, download dependencies.
COPY go.mod go.sum ./
RUN go mod download

# Copy the rest of the source code.
COPY . .

# Build the Go binary.
# Using CGO_ENABLED=0 to produce a fully static binary.
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o app .

# ---------------------------
# Final Stage
# ---------------------------
FROM alpine:latest

# Install CA certificates for HTTPS support (if needed).
RUN apk --no-cache add ca-certificates

# Set working directory.
WORKDIR /root/

# Copy the compiled binary and static assets from the builder.
COPY --from=builder /app/app .
COPY --from=builder /app/static ./static

# Expose the port the app listens on.
EXPOSE 8080

# Start the application.
CMD ["./app"]

