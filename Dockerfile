# Use the official Golang image for building the application
FROM golang:1.25.6 AS builder

# Set the working directory inside the container
WORKDIR /app

# Copy the Go modules manifest and download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code into the container
COPY . .

# Ensures a statically linked binary
ENV CGO_ENABLED=0

# Build the Go server
RUN go build -mod=readonly -o server .

# Use a minimal base image for running the compiled binary
FROM gcr.io/distroless/base-debian13

# Copy the built server binary into the runtime container
COPY --from=builder /app/server /server

# Expose the port that the server will listen on
EXPOSE 8080

# Run the server binary
CMD ["/server"]
