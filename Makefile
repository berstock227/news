.PHONY: help build run test clean docker-build docker-run docker-stop proto lint

# Default target
help:
	@echo "Available commands:"
	@echo "  build        - Build the application"
	@echo "  run          - Run the application locally"
	@echo "  test         - Run tests"
	@echo "  clean        - Clean build artifacts"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Run with Docker Compose"
	@echo "  docker-stop  - Stop Docker Compose"
	@echo "  proto        - Generate Protocol Buffers"
	@echo "  lint         - Run linter"
	@echo "  deps         - Download dependencies"

# Build the application
build:
	@echo "Building application..."
	go build -o bin/chat-app cmd/server/main.go

# Run the application locally
run:
	@echo "Running application..."
	go run cmd/server/main.go

# Run tests
test:
	@echo "Running tests..."
	go test -v ./...

# Run tests with coverage
test-coverage:
	@echo "Running tests with coverage..."
	go test -v -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf bin/
	rm -f coverage.out coverage.html

# Download dependencies
deps:
	@echo "Downloading dependencies..."
	go mod download
	go mod tidy

# Generate Protocol Buffers
proto:
	@echo "Generating Protocol Buffers..."
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/chat.proto

# Run linter
lint:
	@echo "Running linter..."
	golangci-lint run

# Build Docker image
docker-build:
	@echo "Building Docker image..."
	docker build -t chat-app .

# Test Docker build
docker-test:
	@echo "Testing Docker build..."
	chmod +x build-test.sh && ./build-test.sh

# Run with Docker Compose
docker-run:
	@echo "Starting services with Docker Compose..."
	docker-compose up -d

# Stop Docker Compose
docker-stop:
	@echo "Stopping Docker Compose..."
	docker-compose down

# View logs
docker-logs:
	docker-compose logs -f

# Clean Docker
docker-clean:
	@echo "Cleaning Docker resources..."
	docker-compose down -v --remove-orphans
	docker system prune -f

# Database migrations (placeholder)
migrate:
	@echo "Running database migrations..."
	# Add migration commands here

# Seed database (placeholder)
seed:
	@echo "Seeding database..."
	# Add seed commands here

# Development setup
dev-setup: deps proto
	@echo "Development setup complete!"

# Production build
prod-build: clean deps proto build
	@echo "Production build complete!"

# Install development tools
install-tools:
	@echo "Installing development tools..."
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

# Format code
fmt:
	@echo "Formatting code..."
	go fmt ./...

# Vet code
vet:
	@echo "Vetting code..."
	go vet ./...

# Security check
security:
	@echo "Running security checks..."
	gosec ./...

# Performance benchmark
bench:
	@echo "Running benchmarks..."
	go test -bench=. ./...

# Generate documentation
docs:
	@echo "Generating documentation..."
	godoc -http=:6060

# Check for updates
check-updates:
	@echo "Checking for dependency updates..."
	go list -u -m all

# Update dependencies
update-deps:
	@echo "Updating dependencies..."
	go get -u ./...
	go mod tidy
