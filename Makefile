BINARY=deploi
BUILD_DIR=build
VERSION=$(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS=-ldflags="-X 'github.com/v0id00/deploi/internal/app.version=$(VERSION)'"

.PHONY: build clean install test lint run tidy

build:
	@mkdir -p $(BUILD_DIR)
	go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY) ./cmd/deploi/
	@echo "✓ Built $(BUILD_DIR)/$(BINARY) ($(VERSION))"

build-all:
	@mkdir -p $(BUILD_DIR)
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-amd64 ./cmd/deploi/
	GOOS=linux GOARCH=386 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-x86 ./cmd/deploi/
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-linux-arm64 ./cmd/deploi/
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-macos-amd64 ./cmd/deploi/
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-macos-arm64 ./cmd/deploi/
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-windows-amd64.exe ./cmd/deploi/
	GOOS=windows GOARCH=386 go build $(LDFLAGS) -o $(BUILD_DIR)/$(BINARY)-windows-x86.exe ./cmd/deploi/
	@echo "✓ Built all platforms ($(VERSION))"

install:
	go install -ldflags="-X 'github.com/v0id00/deploi/internal/app.version=$(VERSION)'" \
		./cmd/deploi/
	@echo "✓ Installed $(BINARY) to $$GOPATH/bin/$(BINARY)"

clean:
	rm -rf $(BUILD_DIR)
	@echo "✓ Cleaned"

test:
	go test ./...

lint:
	@which golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed"; exit 1; }
	golangci-lint run ./...

run: build
	./$(BUILD_DIR)/$(BINARY) $(ARGS)

tidy:
	go mod tidy

update-deps:
	go get -u ./...
	go mod tidy
