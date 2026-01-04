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
	rm -f cookies testserver

TAG ?= github.com/what-is-water93/cookies

.PHONY: docker-build
docker-build:
	docker build -t $(TAG) .

.PHONY: docker-test
docker-test:
	docker run --rm  $(TAG) mise exec -- go test -v

.PHONY: lint
lint:
	golangci-lint run
