FROM golang:1.26.4-alpine3.23 AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY main.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/whmcs-zoom-sync .
RUN GOBIN=/out go install github.com/aptible/supercronic@v0.2.47

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S -g 65532 app \
    && adduser -S -D -H -u 65532 -G app app

COPY --from=build --chown=app:app /out/whmcs-zoom-sync /usr/local/bin/whmcs-zoom-sync
COPY --from=build --chown=app:app /out/supercronic /usr/local/bin/supercronic
COPY --chown=app:app crontab /etc/whmcs-zoom-sync.crontab

RUN supercronic -test /etc/whmcs-zoom-sync.crontab

USER app
WORKDIR /app

ENTRYPOINT ["supercronic", "-split-logs", "/etc/whmcs-zoom-sync.crontab"]
