FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS=linux
ARG TARGETARCH=amd64
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/pocket-relay .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata rclone \
    && addgroup -g 10001 relay && adduser -D -H -u 10001 -G relay relay \
    && mkdir -p /data && chown relay:relay /data
COPY --from=build /out/pocket-relay /usr/local/bin/pocket-relay
COPY LICENSE /usr/share/licenses/pocket-relay/LICENSE
WORKDIR /app
ENV DATA_DIR=/data LISTEN_ADDR=:8080 TZ=Asia/Shanghai
USER 10001:10001
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD wget -q -O /dev/null http://127.0.0.1:8080/healthz || exit 1
ENTRYPOINT ["/usr/local/bin/pocket-relay"]
