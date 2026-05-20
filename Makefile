VER := $(shell git describe --tag)
SHA1 := $(shell git rev-parse HEAD)
NOW := $(shell date -u +'%Y-%m-%d_%TZ')
GOBIN := $(shell go env GOPATH)/bin

.PHONY: fmt vet lint sec vuln test-race verify cover
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

# SEC-014: govulncheck scans the build configuration against the Go
# vuln DB. Stdlib findings track go.mod's toolchain directive, so a
# stdlib finding here means the toolchain pin is stale (bump it and
# the finding clears). Pinned in tools: to a known version for
# reproducibility — bump intentionally, not via @latest drift.
vuln:
	@echo "==> govulncheck"
	@command -v govulncheck >/dev/null 2>&1 && govulncheck ./... || $(GOBIN)/govulncheck ./...

test-race:
	@echo "==> go test -race"
	go test -race -count=1 -timeout 60s ./...

verify: fmt vet lint sec vuln test-race openapi-check
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

# IMG_NAME / IMG_TAG default to the local-overlay reference. The
# deploy/overlays/local Kustomization rewrites ghcr.io/sksmith/go-micro-example
# to this name+tag; `make docker-load` (K8S-002) builds with the same
# tag and loads the result into kind's containerd so the kubelet can
# find it under `imagePullPolicy: Never`. CI builds (e.g. goreleaser
# in OPS-007) override these.
IMG_NAME ?= go-micro-example
IMG_TAG  ?= dev

docker:
	@echo Building the docker image
	docker build \
		--build-arg VER=$(VER) \
		--build-arg SHA1=$(SHA1) \
		--build-arg NOW=$(NOW) \
		-t $(IMG_NAME):$(IMG_TAG) .

tools:
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install github.com/swaggo/swag/v2/cmd/swag@latest
	go install github.com/oapi-codegen/oapi-codegen/v2/cmd/oapi-codegen@latest
	# OPS-003: gofumpt enforces stricter Go formatting; the
	# pre-commit hook and golangci-lint formatter both depend on it.
	go install mvdan.cc/gofumpt@latest
	# govulncheck is pinned (not @latest) so the tool surface that
	# decides whether `make verify` passes is reproducible across
	# developer machines and CI. Bump intentionally; the other tools
	# above are still @latest pending a similar pass.
	go install golang.org/x/vuln/cmd/govulncheck@v1.3.0

# OPS-003: install pre-commit hooks against .pre-commit-config.yaml.
# Requires the `pre-commit` CLI (brew install pre-commit on macOS,
# pip install pre-commit elsewhere). The hooks shell out to gofumpt
# and golangci-lint, both installed by `make tools`.
.PHONY: precommit-install
precommit-install:
	@command -v pre-commit >/dev/null 2>&1 || { echo "pre-commit not found. Install via 'brew install pre-commit' (macOS) or 'pip install pre-commit'."; exit 1; }
	pre-commit install

.PHONY: openapi clients clients-go clients-ts openapi-check
openapi:
	@echo "==> regenerating internal/app/openapi.yaml from handler annotations"
	$(GOBIN)/swag init -g cmd/server/main.go -d ./ -o internal/app/_swag --v3.1 --parseDependency --parseDepth 2 --quiet
	@mv internal/app/_swag/swagger.yaml internal/app/openapi.yaml
	@mv internal/app/_swag/swagger.json internal/app/openapi.json
	@rm -rf internal/app/_swag

clients: clients-go clients-ts

clients-go:
	@echo "==> regenerating Go client at api/client/v1"
	@mkdir -p api/client/v1
	$(GOBIN)/oapi-codegen -package v1 -generate types,client internal/app/openapi.yaml > api/client/v1/client.gen.go

clients-ts:
	@echo "==> regenerating TS client at web/src/api"
	@mkdir -p web/src/api
	@command -v npx >/dev/null 2>&1 || { echo "npx not found — install Node.js to regenerate the TS client"; exit 1; }
	npx --yes openapi-typescript@7 internal/app/openapi.yaml -o web/src/api/schema.ts

# openapi-check is the CI drift gate: regenerate the spec and Go
# client, then fail if the working tree differs from committed.
# TS client is generated locally only (Node is not in the Go CI image);
# its drift is reviewed by hand for now.
openapi-check: openapi clients-go
	@git diff --exit-code -- internal/app/openapi.yaml internal/app/openapi.json api/client/v1 \
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

# Local Kubernetes workflow: two commands, all-or-nothing.
#
# `make k8s-up` stands up a single-node kind cluster and applies
# every K8S-* manifest in one shot — kind + image load + ESO +
# Vault + dependencies (Postgres, RabbitMQ, OTel collector) +
# every UI pod (pgAdmin, Jaeger, Loki + Promtail + Grafana,
# Prometheus, Headlamp). When `kubectl rollout status` returns
# the whole stack is Ready and the base ExternalSecret has
# resolved through ESO + Vault. Cold bring-up is ~3-5 min.
#
# `make k8s-down` deletes the kind cluster; everything in it
# goes with it. There are no half-up states.
#
# To reach a UI pod, port-forward its Service directly:
#   kubectl -n go-micro-example port-forward svc/<name> <local>:<svc>
# See deploy/README.md for the Service-name / port table.
#
# Prereqs (local): kind, kubectl, docker. See deploy/README.md
# for install hints. CI installs them via helm/kind-action and
# the default ubuntu-latest tooling.
.PHONY: k8s-up k8s-down
KIND_CLUSTER ?= go-micro-example
KIND_CONFIG  ?= deploy/kind/cluster.yaml

k8s-up: docker
	@command -v kind >/dev/null 2>&1 || { echo "kind not found. Install via 'brew install kind' (macOS) or see https://kind.sigs.k8s.io/docs/user/quick-start/#installation."; exit 1; }
	kind create cluster --name $(KIND_CLUSTER) --config $(KIND_CONFIG)
	# K8S-002: load the locally-built image into kind's containerd so
	# the overlay's `imagePullPolicy: Never` resolves.
	kind load docker-image $(IMG_NAME):$(IMG_TAG) --name $(KIND_CLUSTER)
	# kube-system add-ons land outside the overlay so the overlay's
	# `namespace:` directive doesn't rewrite them. metrics-server
	# (K8S-004) powers the HPA + `kubectl top`; kube-network-policies
	# (K8S-003) makes the OPS-004 NetworkPolicy actually drop traffic
	# on kind (kindnet has no enforcement of its own).
	kubectl apply -f deploy/overlays/local/metrics-server.yaml
	kubectl apply -f deploy/overlays/local/kube-network-policies.yaml
	# External Secrets Operator. Server-side apply because the CRD
	# bundle exceeds kubectl's 256 KiB last-applied annotation; the
	# controller + webhook rollouts must finish before the
	# kustomize render below creates the ClusterSecretStore (the
	# webhook would reject it otherwise).
	kubectl apply --server-side -f deploy/overlays/local/external-secrets-operator.yaml
	kubectl -n default rollout status deploy/external-secrets --timeout=180s
	kubectl -n default rollout status deploy/external-secrets-webhook --timeout=180s
	# The single overlay. --load-restrictor=LoadRestrictionsNone is
	# needed for the configMapGenerator that reads scripts/rabbitmq/*
	# from outside the overlay directory.
	kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/overlays/local | kubectl apply -f -
	kubectl -n go-micro-example rollout status deploy/vault --timeout=120s
	kubectl -n go-micro-example wait --for=condition=complete job/vault-bootstrap --timeout=120s
	kubectl -n go-micro-example rollout status deploy/go-micro-example --timeout=300s
	kubectl -n kube-system rollout status deploy/metrics-server --timeout=180s
	kubectl -n kube-system rollout status ds/kube-network-policies --timeout=120s

k8s-down:
	@command -v kind >/dev/null 2>&1 || { echo "kind not found."; exit 1; }
	kind delete cluster --name $(KIND_CLUSTER)

# k8s-deploy-app rebuilds the app image, reloads it into the running
# kind cluster, and triggers a rolling restart of the go-micro-example
# Deployment. The image tag stays $(IMG_TAG) (default `dev`) so the
# overlay's `imagePullPolicy: Never` keeps resolving — kubelet would
# otherwise try to pull from ghcr if the tag rotated. Because the tag
# is unchanged Kubernetes wouldn't notice the new bytes on its own, so
# `kubectl rollout restart` forces a fresh pod that picks the new
# image up off containerd. Use this after editing app code; for
# manifest changes apply the overlay directly with the kustomize line
# from `make k8s-up`.
.PHONY: k8s-deploy-app
k8s-deploy-app: docker
	@command -v kind >/dev/null 2>&1 || { echo "kind not found."; exit 1; }
	kind load docker-image $(IMG_NAME):$(IMG_TAG) --name $(KIND_CLUSTER)
	kubectl -n go-micro-example rollout restart deploy/go-micro-example
	kubectl -n go-micro-example rollout status  deploy/go-micro-example --timeout=180s