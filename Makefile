VER := $(shell git describe --tag)
SHA1 := $(shell git rev-parse HEAD)
NOW := $(shell date -u +'%Y-%m-%d_%TZ')
GOBIN := $(shell go env GOPATH)/bin

.PHONY: fmt vet lint sec test-race verify cover
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
	@command -v gosec >/dev/null 2>&1 && gosec -exclude-dir=api/client ./... || $(GOBIN)/gosec -exclude-dir=api/client ./...

test-race:
	@echo "==> go test -race"
	go test -race -count=1 -timeout 60s ./...

verify: fmt vet lint sec test-race openapi-check
	@echo "==> verify OK"

cover:
	@echo "==> coverage"
	go test ./... -race -coverprofile=cover.out
	@echo "--- uncovered lines ---"
	@go tool cover -func=cover.out | grep -v '100.0%' || true

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
	go install github.com/swaggo/swag/v2/cmd/swag@latest
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest

.PHONY: openapi clients clients-go clients-ts openapi-check
openapi:
	@echo "==> regenerating api/openapi.yaml from handler annotations"
	$(GOBIN)/swag init -g cmd/main.go -d ./ -o api/_swag --v3.1 --parseDependency --parseDepth 2 --quiet
	@mv api/_swag/swagger.yaml api/openapi.yaml
	@mv api/_swag/swagger.json api/openapi.json
	@rm -rf api/_swag

clients: clients-go clients-ts

clients-go:
	@echo "==> regenerating Go client at api/client/v1"
	@mkdir -p api/client/v1
	$(GOBIN)/oapi-codegen -package v1 -generate types,client api/openapi.yaml > api/client/v1/client.gen.go

clients-ts:
	@echo "==> regenerating TS client at web/src/api"
	@mkdir -p web/src/api
	@command -v npx >/dev/null 2>&1 || { echo "npx not found — install Node.js to regenerate the TS client"; exit 1; }
	npx --yes openapi-typescript@7 api/openapi.yaml -o web/src/api/schema.ts

# openapi-check is the CI drift gate: regenerate the spec and Go
# client, then fail if the working tree differs from committed.
# TS client is generated locally only (Node is not in the Go CI image);
# its drift is reviewed by hand for now.
openapi-check: openapi clients-go
	@git diff --exit-code -- api/openapi.yaml api/openapi.json api/client/v1 \
	  || { echo "OpenAPI artifacts are stale. Run 'make openapi clients' and commit."; exit 1; }

.PHONY: demo demo-down
# demo runs the DSN-015 orchestrator end-to-end against a fresh
# docker-compose stack. Only the app + demo log streams are attached
# so the reader sees the orchestrator's summary and the app's own
# output, not Postgres/RabbitMQ/pgadmin noise. The exit code of the
# demo container is captured and propagated, and demo-down runs
# unconditionally (trap on EXIT) so a successful or failed demo
# leaves no dangling containers.
demo:
	@set -e; trap '$(MAKE) demo-down' EXIT; \
	docker compose up --build --abort-on-container-exit --exit-code-from demo \
	  --attach app --attach demo

demo-down:
	docker compose down -v