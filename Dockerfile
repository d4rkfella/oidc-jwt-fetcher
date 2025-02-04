FROM alpine:3.21

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
