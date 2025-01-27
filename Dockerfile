FROM alpine:3.21.2@sha256:56fa17d2a7e7f168a043a2712e63aed1f8543aeafdcee47c58dcffe38ed51099

USER root
WORKDIR /app

RUN apk update && apk add --no-cache \
    curl \
    ca-certificates \
    jq \
    bash \
    catatonit \
    kubectl

COPY entrypoint.sh /entrypoint.sh

RUN chmod +x /entrypoint.sh

USER nobody:nogroup

ENTRYPOINT ["/usr/bin/catatonit", "--", "/entrypoint.sh"]
