GO ?= go
EXT ?=

.PHONY: all build test clean

all: build

build:
	$(GO) build -v -o integration-github-importer$(EXT) ./importer

test:
	$(GO) test -v ./...

clean:
	rm -f integration-github-importer$(EXT)
