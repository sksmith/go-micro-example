VER := $(shell git describe --tag)
SHA1 := $(shell git rev-parse HEAD)
NOW := $(shell date -u +'%Y-%m-%d_%TZ')

build:
	@echo Building the binary
	go build -ldflags "-X main.AppVersion=$(VER) -X main.Sha1Version=$(SHA1) -X main.BuildTime=$(NOW)" -o ./bin/inventory ./cmd/app

test:
	go test -v ./...

run:
	echo "executing the application"
	go run ./cmd/app/.
