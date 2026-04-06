GO ?= go
EXT ?=

.PHONY: all build test clean

all: build

build:
	$(GO) build -v -o plakar-github-importer$(EXT) ./importer

test:
	$(GO) test -v ./...

clean:
	rm -f plakar-github-importer$(EXT)
