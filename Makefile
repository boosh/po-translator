.PHONY: all build test install clean

# Variables
BINARY_NAME=po-translator

all: build

# Build the application
build:
	@echo "Building $(BINARY_NAME)..."
	@go build -o $(BINARY_NAME) .

# Run tests
test:
	@echo "Running tests..."
	@go test ./...

# Install the application
install:
	@echo "Installing $(BINARY_NAME)..."
	@go install .

# Clean build artifacts
clean:
	@echo "Cleaning..."
	@if [ -f $(BINARY_NAME) ]; then rm $(BINARY_NAME); fi
	@go clean -testcache