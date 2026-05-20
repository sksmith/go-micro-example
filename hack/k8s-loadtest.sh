#!/usr/bin/env bash
# K8S-004: drive the go-micro-example pod's CPU above the HPA's 70%
# utilisation target so we can observe scale-up against the local
# kind cluster.
#
# Spawns a single in-cluster pod that fires `parallelism` concurrent
# wgets against the Service in tight loops for `duration` seconds.
# The pod is labelled `app.kubernetes.io/component: loadtest` so the
# K8S-004 NetworkPolicy patch lets it reach :8080 on the app pod
# (the base policy only allows ingress from ingress-nginx /
# prometheus labels).
#
# Usage: hack/k8s-loadtest.sh [duration_seconds] [parallelism]
#   duration    seconds the load runs for (default 180)
#   parallelism concurrent wgets per "wave" (default 12)
#
# The pod self-deletes when the loop exits (or when you Ctrl-C).
# Watch the HPA in another terminal:
#     kubectl -n go-micro-example get hpa -w

set -euo pipefail

DURATION="${1:-180}"
PARALLEL="${2:-12}"
NS=go-micro-example
TARGET="http://go-micro-example/live"

echo "==> driving ${PARALLEL} concurrent wgets at ${TARGET} for ${DURATION}s"
echo "    (watch \`kubectl -n ${NS} get hpa -w\` in another terminal)"

# Pin the busybox digest so the loop is reproducible across runs.
# Bump intentionally; the script logs the tag so a future drift is
# obvious.
BUSYBOX_IMAGE="busybox:1.37"

kubectl -n "$NS" run k8s-loadtest \
  --rm -i --restart=Never \
  --image="$BUSYBOX_IMAGE" \
  --labels='app.kubernetes.io/component=loadtest' \
  --command -- \
  /bin/sh -c "
    end=\$((\$(date +%s) + ${DURATION}))
    waves=0
    while [ \$(date +%s) -lt \$end ]; do
      for i in \$(seq 1 ${PARALLEL}); do
        wget -qO- ${TARGET} >/dev/null 2>&1 &
      done
      wait
      waves=\$((waves + 1))
    done
    echo \"drove \$waves waves of ${PARALLEL} concurrent requests\"
  "
