FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/pocket-relay .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/data-init ./cmd/data-init

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata rclone su-exec \
    && addgroup -g 10001 relay && adduser -D -H -u 10001 -G relay relay \
    && mkdir -p /data && chown relay:relay /data
COPY --from=build /out/pocket-relay /usr/local/bin/pocket-relay
COPY --from=build /out/data-init /usr/local/bin/data-init
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY LICENSE /usr/share/licenses/pocket-relay/LICENSE
WORKDIR /app
ENV DATA_DIR=/data LISTEN_ADDR=:8080 TZ=Asia/Shanghai
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
