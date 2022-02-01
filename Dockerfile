FROM golang:alpine as builder

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
