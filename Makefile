VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null || echo "dev")
COMMIT := $(shell git rev-parse --short HEAD 2>/dev/null || echo "unknown")

LDFLAGS := -X main.version=$(VERSION) -X main.commit=$(COMMIT)

.PHONY: build
build:
	go build -ldflags "$(LDFLAGS)" -o cookies

.PHONY: build-release
build-release:
	go build -ldflags "-s -w $(LDFLAGS)" -o cookies

.PHONY: clean
clean:
	rm -f cookies
