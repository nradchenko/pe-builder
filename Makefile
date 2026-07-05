# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0

BINARY  := pebuild
BINDIR  := bin
PKG     := github.com/nradchenko/pe-builder/internal/buildinfo

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

.PHONY: all build test vet lint fmt tidy clean

all: build

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$(BINARY) ./cmd/pebuild

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run

fmt:
	go fmt ./...

tidy:
	go mod tidy

clean:
	rm -rf $(BINDIR)
