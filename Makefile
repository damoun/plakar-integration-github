GO ?= go
EXT ?=
OS   := $(shell go env GOOS)
ARCH := $(shell go env GOARCH)
NAME := integration-github
VERSION := $(shell grep '^version:' manifest.yaml | awk '{print $$2}')
PTAR := $(NAME)_$(VERSION)_$(OS)_$(ARCH).ptar

.PHONY: all build test pkg install clean

all: build

build:
	$(GO) build -v -o $(NAME)-importer$(EXT) ./importer

test:
	$(GO) test -race -v ./...

pkg: build
	rm -f $(PTAR)
	plakar pkg create manifest.yaml

install: pkg
	plakar pkg rm $(NAME) 2>/dev/null || true
	plakar pkg add ./$(PTAR)

clean:
	rm -f $(NAME)-importer$(EXT) *.ptar coverage.out
