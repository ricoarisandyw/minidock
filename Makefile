VERSION ?= dev
LDFLAGS := -s -w -X main.version=$(VERSION)
BIN     := dist/minidock

.PHONY: run test test-integration vet fmt build build-all install uninstall clean

run:
	go run ./cmd/minidock

test:
	go test ./...

# Needs a running Docker; pulls redis:7-alpine and mysql:8.4 and cleans up after itself.
test-integration:
	go test -tags integration -count=1 -v ./internal/docker ./internal/mcpserver

vet:
	go vet ./...

fmt:
	gofmt -l -w cmd internal

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/minidock

# One binary per OS/CPU in dist/.
build-all:
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/minidock-darwin-arm64 ./cmd/minidock
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/minidock-darwin-amd64 ./cmd/minidock
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/minidock-linux-amd64 ./cmd/minidock
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/minidock-linux-arm64 ./cmd/minidock
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/minidock-windows-amd64.exe ./cmd/minidock

# Install to ~/.local/bin by default. For all users: make install PREFIX=/usr/local (may need sudo).
PREFIX ?= $(HOME)/.local

# On Apple Silicon every binary must carry a valid signature, and a binary the
# kernel refuses is killed on sight: "zsh: killed", with no other message.
# Signing the installed copy ad-hoc keeps that from happening.
install: build
	mkdir -p $(PREFIX)/bin
	cp $(BIN) $(PREFIX)/bin/minidock
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		xattr -d com.apple.quarantine $(PREFIX)/bin/minidock 2>/dev/null || true; \
		codesign --force --sign - $(PREFIX)/bin/minidock 2>/dev/null || true; \
	fi
	@echo "Installed to $(PREFIX)/bin/minidock"
	@case ":$$PATH:" in *":$(PREFIX)/bin:"*) ;; *) echo "Add this to ~/.zshrc, then open a new terminal:  export PATH=\"$(PREFIX)/bin:\$$PATH\"" ;; esac

uninstall:
	rm -f $(PREFIX)/bin/minidock

clean:
	rm -rf dist
