BINARY  := goforge
PKG     := ./cmd/goforge
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/Imanuelkarl/forge/internal/cli.version=$(VERSION)

.PHONY: install build test vet fmt clean

## install: build and install goforge into $(go env GOPATH)/bin (put that on your PATH)
install:
	go install -ldflags="$(LDFLAGS)" $(PKG)

## build: build ./bin/goforge
build:
	go build -ldflags="$(LDFLAGS)" -o bin/$(BINARY)$(shell go env GOEXE) $(PKG)

test:
	go test ./... -count=1

vet:
	go vet ./...

fmt:
	gofmt -w .

clean:
	rm -rf bin
