.PHONY: tidy build build-windows build-windows-arm64 build-linux build-mac run-agent run-stub test clean

BIN_DIR := dist
AGENT := agent
STUB := saas-stub

tidy:
	go mod tidy

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(AGENT) ./cmd/agent
	go build -o $(BIN_DIR)/$(STUB) ./cmd/saas-stub

build-windows:
	mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o $(BIN_DIR)/$(AGENT).exe ./cmd/agent

build-windows-arm64:
	mkdir -p $(BIN_DIR)
	GOOS=windows GOARCH=arm64 go build -ldflags "-s -w" -o $(BIN_DIR)/$(AGENT)-arm64.exe ./cmd/agent

build-linux:
	mkdir -p $(BIN_DIR)
	GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o $(BIN_DIR)/$(AGENT)-linux-amd64 ./cmd/agent

build-mac:
	mkdir -p $(BIN_DIR)
	GOOS=darwin GOARCH=arm64 go build -ldflags "-s -w" -o $(BIN_DIR)/$(AGENT)-darwin-arm64 ./cmd/agent

run-agent:
	go run ./cmd/agent

run-stub:
	go run ./cmd/saas-stub

test:
	go test ./...

clean:
	rm -rf $(BIN_DIR)
