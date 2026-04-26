# Multi-stage build per PART 27
FROM golang:alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY src/ ./src/
ARG VERSION=dev
ARG BUILD_DATE
ARG GIT_COMMIT
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.Version=${VERSION} -X main.BuildDate=${BUILD_DATE} -X main.GitCommit=${GIT_COMMIT}" \
    -o casgist ./src/cmd/casgists

FROM alpine:latest
RUN apk add --no-cache bash curl tini tzdata ca-certificates
RUN addgroup -g 1001 -S casgist && \
    adduser -u 1001 -S casgist -G casgist
COPY --from=builder /build/casgist /usr/local/bin/casgist
COPY docker/rootfs/ /
RUN mkdir -p /data /config /var/log/casgist && \
    chown -R casgist:casgist /data /config /var/log/casgist
USER casgist
EXPOSE 80
WORKDIR /data
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["/usr/local/bin/casgist"]
LABEL org.opencontainers.image.title="casgist" \
      org.opencontainers.image.description="Self-hosted GitHub Gist alternative" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.revision="${GIT_COMMIT}" \
      org.opencontainers.image.source="https://github.com/casapps/casgist"
