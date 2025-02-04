# syntax=docker/dockerfile:1

# ---------------------------
# Build Stage
# ---------------------------
FROM golang:1.23-alpine AS builder

# Install git if needed by dependencies.
RUN apk update && apk add --no-cache git

# Set the working directory.
WORKDIR /app

# Copy go.mod and go.sum, then download dependencies.
COPY go.mod go.sum ./
RUN go mod download

# Copy the remaining source code.
COPY . .

# Build the Go binary with CGO disabled.
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o app .

# ---------------------------
# Final Stage
# ---------------------------
FROM alpine:latest

# Install CA certificates for HTTPS support.
RUN apk --no-cache add ca-certificates

# Set working directory.
WORKDIR /root/

# Copy the compiled binary and static assets from the builder.
COPY --from=builder /app/app .
COPY --from=builder /app/static ./static

# Expose the port that the application listens on.
EXPOSE 8080

# Start the application.
CMD ["./app"]

