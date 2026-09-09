# po-translator (Go CLI for Django/gettext .po files)
set dotenv-load := true

binary := "po-translator"

# Build the binary
build: test
    go build -o {{binary}} .

# Install to $(go env GOPATH)/bin
install: test
    go install .

# Run the translator against glob patterns (builds first). The provider comes from
# whichever API key is set in .env; the model comes from LLM_MODEL or --model.
# e.g. just run --fix --dedupe --revert-if-unchanged '*/locale/**/django.po'
run *ARGS: build
    ./{{binary}} {{ARGS}}

# Run without building — dry run with debug logging, so no file is ever written
dev *ARGS: test
    go run . --dry-run --log-level=debug --log-prompt {{ARGS}}

# Run tests
test *ARGS='':
    go test ./... {{ARGS}}

# Run tests with verbose output
test-v *ARGS='':
    go test -v ./... {{ARGS}}

# Remove build artefacts
clean:
    rm -f {{binary}}
    go clean -testcache
    go clean ./...

# Format all Go files
fmt:
    gofmt -w .

# Run the linter
lint:
    go vet ./...

# Tidy go.mod and go.sum
tidy:
    go mod tidy

# Download dependencies
deps:
    go mod download

# Full check: format, lint, test
check: fmt lint test
