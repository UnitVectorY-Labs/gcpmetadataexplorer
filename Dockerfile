# Use the official Golang image for building the application
FROM golang:1.26.0 AS builder

# Set the working directory inside the container
WORKDIR /app

# Build argument for version injection
ARG VERSION=dev

# Copy the Go modules manifest and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code into the container
COPY . .

# Ensures a statically linked binary
ENV CGO_ENABLED=0

# Build the Go server with version injection
RUN go build -mod=readonly -o server -ldflags "-X 'main.Version=${VERSION}'" .

# Use a minimal base image for running the compiled binary
FROM gcr.io/distroless/base-debian13

# Copy the built server binary into the runtime container
COPY --from=builder /app/server /server

# Expose the port that the server will listen on
EXPOSE 8080

# Run as non-root user
USER 65532:65532

# Run the server binary
CMD ["/server"]
