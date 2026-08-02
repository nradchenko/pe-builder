# Copyright 2026 Nikita Radchenko
# SPDX-License-Identifier: Apache-2.0

BINARIES := pebuild pewalk
BINDIR   := bin
PKG      := github.com/nradchenko/pe-builder/internal/buildinfo

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS := -X $(PKG).Version=$(VERSION) -X $(PKG).Commit=$(COMMIT) -X $(PKG).Date=$(DATE)

# Plugin directories that ship C source (Win32 GUI / WDM drivers, MinGW-cross-compiled).
CPLUGINS := $(sort $(dir $(wildcard plugins/*/*.c)))

.PHONY: all build test vet lint lint-c fmt tidy licenses clean

all: build

build:
	@for bin in $(BINARIES); do \
		go build -ldflags "$(LDFLAGS)" -o $(BINDIR)/$$bin ./cmd/$$bin || exit 1; \
	done

test:
	go test ./...

vet:
	go vet ./...

lint:
	golangci-lint run

# lint-c runs the C linter (cppcheck) over every plugin that ships C — Windows-targeted,
# MinGW-cross-compiled — with the shared suppressions for class-level false positives.
lint-c:
	@for d in $(CPLUGINS); do \
		d=$${d%/}; \
		echo "cppcheck $$d"; \
		cppcheck --enable=warning,performance,portability,style --library=windows \
			--inline-suppr --suppressions-list=.cppcheck-suppressions --error-exitcode=1 \
			-I "$$d" "$$d"/*.c || exit 1; \
	done

fmt:
	go fmt ./...

tidy:
	go mod tidy

licenses:
	./scripts/gen-licenses.sh

clean:
	rm -rf $(BINDIR)
