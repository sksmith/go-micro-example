VER := $(shell git describe --tag)
SHA1 := $(shell git rev-parse HEAD)
NOW := $(shell date -u +'%Y-%m-%d_%TZ')

check:
	@echo Linting
	golangci-lint run

	@echo Security scanning
	gosec ./...

	@echo Testing
	go test ./...

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
	go install github.com/securego/gosec/v2/cmd/gosec
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@v1.43.0