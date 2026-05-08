# DEP-001: pin Go to a major.minor (currently 1.24, matching go.mod and
# test.yml). Builds will pick up the latest 1.24.x patch on rebuild,
# which is intentional — patches close stdlib CVEs that govulncheck
# tracks in SEC-008. OPS-002 will tighten this to a digest pin once
# the image supply-chain story lands.
FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY . .

ARG VER=NOT_SUPPLIED
ARG SHA1=NOT_SUPPLIED
ARG NOW=NOT_SUPPLIED

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags "-X github.com/sksmith/go-micro-example/config.AppVersion=$VER \
    -X github.com/sksmith/go-micro-example/config.Sha1Version=$SHA1 \
    -X github.com/sksmith/go-micro-example/config.BuildTime=$NOW" \
    -o ./go-micro-example ./cmd

RUN apk add --update ca-certificates

FROM scratch

WORKDIR /app

COPY --from=builder /app/go-micro-example /usr/bin/
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/config.yml /app

ENTRYPOINT ["go-micro-example"]
