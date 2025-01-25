FROM alpine:3.21.2@sha256:56fa17d2a7e7f168a043a2712e63aed1f8543aeafdcee47c58dcffe38ed51099

ARG USER_UID=65532
ARG GROUP_UID=65532

RUN addgroup -g ${GROUP_UID} -S tokencrew
RUN adduser -u ${USER_UID} -G tokencrew -S tokenmaster
RUN apk update && apk add --no-cache \
    curl \
    ca-certificates \
    jq \
    bash \
    kubectl

COPY get-token.sh /usr/local/bin/get-token.sh

RUN chmod +x /usr/local/bin/get-token.sh

RUN chown ${USER_UID}:${GROUP_UID} /usr/local/bin/get-token.sh

USER ${USER_UID}

ENTRYPOINT ["/usr/local/bin/get-token.sh"]
