BIN      := sitrep
MODULE   := github.com/zebadrabbit/sitrep
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE     ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS  := -s -w -X $(MODULE)/internal/version.Version=$(VERSION) -X $(MODULE)/internal/version.Commit=$(COMMIT) -X $(MODULE)/internal/version.Date=$(DATE)
GOBIN    := $(shell go env GOPATH)/bin
LINT     := $(GOBIN)/golangci-lint
export CGO_ENABLED=0

.PHONY: build run demo test screenshot lint install clean gif

build:
	GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BIN) ./cmd/sitrep
	GOOS=linux GOARCH=arm64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/$(BIN)-arm64 ./cmd/sitrep

run:
	go run ./cmd/sitrep

demo:
	go run ./cmd/sitrep --demo

test:
	go test ./...

# One text frame per tab at both reference sizes. Non-tty output strips ANSI,
# so diffs in PRs show layout changes, not color changes.
screenshot: build
	@mkdir -p docs/screens
	@for tab in overview ports system services cron disks network docker samba nfs sessions logs updates; do for size in 100x30 80x24; do \
	  ./dist/$(BIN) --once --demo --size $$size $$tab > docs/screens/$$tab-$$size.txt; \
	done; done
	@rm -f docs/screens/shell-*.txt
	@ls docs/screens

lint:
	@test -x $(LINT) || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.13.2
	$(LINT) run ./...

# README gif. Needs vhs (go install github.com/charmbracelet/vhs@latest), ttyd, ffmpeg.
gif: build
	VHS_NO_SANDBOX=true PATH="$(CURDIR)/dist:$(GOBIN):$$PATH" vhs docs/demo.tape

install: build
	install -m 755 dist/$(BIN) ~/.local/bin/$(BIN)

clean:
	rm -rf dist
