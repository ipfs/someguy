# Builder
FROM --platform=${BUILDPLATFORM:-linux/amd64} golang:1.22-bookworm AS builder

LABEL org.opencontainers.image.source=https://github.com/ipfs/someguy
LABEL org.opencontainers.image.documentation=https://github.com/ipfs/someguy#docker
LABEL org.opencontainers.image.description="A standalone delegated /routing/v1 HTTP server for IPFS systems"
LABEL org.opencontainers.image.licenses=MIT+APACHE_2.0

ARG TARGETPLATFORM TARGETOS TARGETARCH

ENV GOPATH      /go
ENV SRC_PATH    $GOPATH/src/github.com/ipfs/someguy
ENV GO111MODULE on
ENV GOPROXY     https://proxy.golang.org

COPY go.* $SRC_PATH/
WORKDIR $SRC_PATH
RUN go mod download

COPY . $SRC_PATH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -o $GOPATH/bin/someguy

# Runner
FROM debian:bookworm-slim

RUN apt-get update && \
  apt-get install --no-install-recommends -y tini ca-certificates curl && \
  rm -rf /var/lib/apt/lists/*

ENV GOPATH      /go
ENV SRC_PATH    $GOPATH/src/github.com/ipfs/someguy
ENV DATA_PATH   /data/someguy

COPY --from=builder $GOPATH/bin/someguy /usr/local/bin/someguy

RUN mkdir -p $DATA_PATH && \
    useradd -d $DATA_PATH -u 1000 -G users ipfs && \
    chown ipfs:users $DATA_PATH
VOLUME $DATA_PATH
WORKDIR $DATA_PATH

USER ipfs
ENTRYPOINT ["tini", "--", "/usr/local/bin/someguy", "start"]
