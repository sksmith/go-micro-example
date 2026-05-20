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

# K8S-001: Local kind cluster + dry-run validation gate.
#
# `k8s-up` stands up a single-node kind cluster pinned to the kube
# version the OPS-004 base targets. `k8s-validate` renders the
# Kustomize base and dry-run-applies it against the live apiserver,
# catching admission/RBAC/label bugs that offline schema validation
# (kubeconform) cannot. `k8s-down` tears the cluster back down.
#
# `ExternalSecret` is filtered before the dry-run because the
# external-secrets.io CRDs are not installed in this Tier 1 cluster
# (the operator install is K8S-005's territory). When K8S-005 lands,
# drop the yq filter so the dry-run covers the full base.
#
# Prereqs (local): kind, kubectl, yq. See deploy/README.md for
# install hints. CI installs them via helm/kind-action and the
# default ubuntu-latest tooling.
.PHONY: k8s-up k8s-down k8s-validate k8s-validate-local docker-load
KIND_CLUSTER  ?= go-micro-example
KIND_CONFIG   ?= deploy/kind/cluster.yaml
# KUSTOMIZE_DIR selects which directory `k8s-validate` renders. The
# default is the OPS-004 base; `k8s-validate-local` re-invokes the
# same target against the K8S-002 local overlay.
KUSTOMIZE_DIR ?= deploy/base

k8s-up:
	@command -v kind >/dev/null 2>&1 || { echo "kind not found. Install via 'brew install kind' (macOS) or see https://kind.sigs.k8s.io/docs/user/quick-start/#installation."; exit 1; }
	kind create cluster --name $(KIND_CLUSTER) --config $(KIND_CONFIG)

k8s-down:
	@command -v kind >/dev/null 2>&1 || { echo "kind not found."; exit 1; }
	kind delete cluster --name $(KIND_CLUSTER)

k8s-validate:
	@command -v kubectl >/dev/null 2>&1 || { echo "kubectl not found."; exit 1; }
	@command -v yq >/dev/null 2>&1 || { echo "yq not found. Install via 'brew install yq' (macOS) or see https://github.com/mikefarah/yq#install."; exit 1; }
	@kubectl get namespace go-micro-example >/dev/null 2>&1 || kubectl create namespace go-micro-example
	# --load-restrictor=LoadRestrictionsNone: the K8S-003 overlay's
	# configMapGenerator reads scripts/rabbitmq/* from outside its
	# own directory (shared source of truth with docker-compose).
	# Harmless for base (which stays inside deploy/base/).
	kubectl kustomize --load-restrictor=LoadRestrictionsNone $(KUSTOMIZE_DIR) | yq 'select(.kind != "ExternalSecret")' | kubectl apply --dry-run=server -f -

k8s-validate-local:
	$(MAKE) k8s-validate KUSTOMIZE_DIR=deploy/overlays/local

# K8S-002: build the app image with the dev tag and load it into the
# kind node's containerd store. The overlay's `imagePullPolicy: Never`
# tells the kubelet to use this image directly — no registry round-trip.
docker-load: docker
	@command -v kind >/dev/null 2>&1 || { echo "kind not found."; exit 1; }
	kind load docker-image $(IMG_NAME):$(IMG_TAG) --name $(KIND_CLUSTER)

# K8S-003: full local stack — kind cluster + image load + the local
# overlay (in-cluster Postgres / RabbitMQ + dev Secret). `rollout
# status` blocks until the app Deployment is Available, which (via
# the readiness probe) means migrations completed and AMQP redial
# succeeded. The 300 s timeout covers the 150 s startup-probe budget
# from DSN-002 plus a margin for image-pull and DB init on a cold
# kind cluster.
.PHONY: k8s-local-up k8s-local-down k8s-local-loadtest
k8s-local-up: k8s-up docker-load
	# K8S-004 metrics-server and the kube-network-policies enforcer
	# land first in kube-system — outside the overlay's kustomization
	# so the overlay's `namespace:` directive doesn't rewrite them.
	# The enforcer makes the OPS-004 NetworkPolicy actually drop
	# disallowed traffic on kind (kindnet does not enforce on its own).
	kubectl apply -f deploy/overlays/local/metrics-server.yaml
	kubectl apply -f deploy/overlays/local/kube-network-policies.yaml
	kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/overlays/local | kubectl apply -f -
	kubectl -n go-micro-example rollout status deploy/go-micro-example --timeout=300s
	kubectl -n kube-system rollout status deploy/metrics-server --timeout=180s
	kubectl -n kube-system rollout status ds/kube-network-policies --timeout=120s

k8s-local-down:
	-kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/overlays/local | kubectl delete --ignore-not-found -f -
	-kubectl delete --ignore-not-found -f deploy/overlays/local/metrics-server.yaml
	-kubectl delete --ignore-not-found -f deploy/overlays/local/kube-network-policies.yaml
	$(MAKE) k8s-down

# K8S-004: drive the app's CPU above the HPA's 70 % target so a
# scale-up event is visible. Defaults to 180 s of load; pass
# different values via:
#     make k8s-local-loadtest LOADTEST_DURATION=120 LOADTEST_PARALLEL=8
LOADTEST_DURATION ?= 180
LOADTEST_PARALLEL ?= 12
k8s-local-loadtest:
	./hack/k8s-loadtest.sh $(LOADTEST_DURATION) $(LOADTEST_PARALLEL)

# K8S-011 / K8S-012: open the in-cluster Grafana. Shared by both
# tickets — K8S-011 provisions a Loki datasource, K8S-012 appends a
# Prometheus datasource, but the entry point is one URL.
.PHONY: k8s-local-ui-grafana
k8s-local-ui-grafana:
	@echo "Grafana: http://localhost:3000  (admin / admin)"
	@command -v open >/dev/null 2>&1 && (sleep 1 && open http://localhost:3000) &
	kubectl -n go-micro-example port-forward svc/grafana 3000:3000

# K8S-012: open the Prometheus expression browser. Useful for
# /targets (annotation-discovered scrape targets) and ad-hoc PromQL
# without the Grafana dashboard wrapper.
.PHONY: k8s-local-ui-prometheus
k8s-local-ui-prometheus:
	@echo "Prometheus: http://localhost:9090  (/targets, /graph)"
	@command -v open >/dev/null 2>&1 && (sleep 1 && open http://localhost:9090) &
	kubectl -n go-micro-example port-forward svc/prometheus 9090:9090

# K8S-005: ESO-flavoured local stack — installs External Secrets
# Operator, brings up an in-cluster dev Vault, seeds the secret
# paths the base ExternalSecret references, and rolls out the app
# pulling its creds through ESO instead of the K8S-003 plain Secret.
.PHONY: k8s-eso-up k8s-eso-down
k8s-eso-up: k8s-up docker-load
	# ESO bundle lands in `default` ns (helm-chart default) — outside
	# the overlay's kustomization so the `namespace:` directive doesn't
	# rewrite it. Server-side apply because two CRDs in the bundle
	# exceed kubectl's 256 KiB last-applied-configuration annotation
	# limit. The controller and webhook rollouts must finish before
	# the kustomize render below tries to create the ClusterSecretStore
	# (the webhook would reject the request otherwise).
	kubectl apply --server-side -f deploy/overlays/local-eso/external-secrets-operator.yaml
	kubectl -n default rollout status deploy/external-secrets --timeout=180s
	kubectl -n default rollout status deploy/external-secrets-webhook --timeout=180s
	kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/overlays/local-eso | kubectl apply -f -
	kubectl -n go-micro-example rollout status deploy/vault --timeout=120s
	kubectl -n go-micro-example wait --for=condition=complete job/vault-bootstrap --timeout=120s
	kubectl -n go-micro-example rollout status deploy/go-micro-example --timeout=300s

k8s-eso-down:
	-kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/overlays/local-eso | kubectl delete --ignore-not-found -f -
	-kubectl delete --ignore-not-found -f deploy/overlays/local-eso/external-secrets-operator.yaml
	$(MAKE) k8s-down

# K8S-005 follow-up: Vault runs in dev mode (in-memory storage), so
# every Vault restart drops the kv state and ESO flips to
# SecretSyncedError. Re-seed by deleting the completed bootstrap
# Job and reapplying the manifest — Job names are static so kubectl
# can't just re-run the existing one. Use after `kubectl scale
# deploy/vault --replicas=0; …=1` or after the Vault pod gets
# evicted.
.PHONY: k8s-eso-reseed
k8s-eso-reseed:
	kubectl -n go-micro-example delete job vault-bootstrap --ignore-not-found
	kubectl kustomize --load-restrictor=LoadRestrictionsNone deploy/overlays/local-eso | kubectl apply -f - >/dev/null
	kubectl -n go-micro-example wait --for=condition=complete job/vault-bootstrap --timeout=60s
	kubectl -n go-micro-example annotate externalsecret go-micro-example-secrets force-sync=$$(date +%s) --overwrite