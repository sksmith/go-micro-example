VER := $(shell git describe --tag)
SHA1 := $(shell git rev-parse HEAD)
NOW := $(shell date -u +'%Y-%m-%d_%TZ')
GOBIN := $(shell go env GOPATH)/bin

.PHONY: fmt vet lint sec test-race verify
fmt:
	@echo "==> gofmt"
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "$$out"; echo "files need gofmt"; exit 1; fi

vet:
	@echo "==> go vet"
	go vet ./...

lint:
	@echo "==> golangci-lint"
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || $(GOBIN)/golangci-lint run

sec:
	@echo "==> gosec"
	@command -v gosec >/dev/null 2>&1 && gosec ./... || $(GOBIN)/gosec ./...

test-race:
	@echo "==> go test -race"
	go test -race -count=1 -timeout 60s ./...

verify: fmt vet lint sec test-race
	@echo "==> verify OK"

build:
	@echo Building the binary
	go build -ldflags "-X github.com/sksmith/go-micro-example/config.AppVersion=$(VER)\
		-X github.com/sksmith/go-micro-example/config.Sha1Version=$(SHA1)\
		-X github.com/sksmith/go-micro-example/config.BuildTime=$(NOW)"\
		-o ./bin/go-micro-example ./cmd

test:
	go test -cover ./...

run:
	echo "executing the application"
	go run ./cmd/.

docker:
	@echo Building the docker image
	docker build \
		--build-arg VER=$(VER) \
		--build-arg SHA1=$(SHA1) .

tools:
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest