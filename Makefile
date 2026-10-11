# Sixter — Build System
# Usage:
#   make windows    → build all binaries for Windows (amd64)
#   make linux      → build all binaries for Linux (amd64)
#   make all        → build both platforms
#   make clean      → remove bin/

MODULE   := sixter.in/tunnel
BIN      := bin

.PHONY: all windows linux clean tidy

all: windows linux

windows:
	@mkdir -p $(BIN)/windows
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/windows/sixter.exe            ./cmd/sixter
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/windows/sixter-client.exe     ./cmd/tunnel-client
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/windows/sixter-server.exe     ./cmd/tunnel-server
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/windows/sixter-repo.exe       ./cmd/repo-client
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/windows/sixter-reposerver.exe ./cmd/repo-server
	@echo "✓ Windows binaries → $(BIN)/windows/"

linux:
	@mkdir -p $(BIN)/linux
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/linux/sixter            ./cmd/sixter
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/linux/sixter-client     ./cmd/tunnel-client
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/linux/sixter-server     ./cmd/tunnel-server
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/linux/sixter-repo       ./cmd/repo-client
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o $(BIN)/linux/sixter-reposerver ./cmd/repo-server
	@echo "✓ Linux binaries → $(BIN)/linux/"

tidy:
	go mod tidy

clean:
	rm -rf $(BIN)
	@echo "✓ Cleaned"
