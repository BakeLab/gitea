FROM docker.io/chainguard/go:latest@sha256:85788137110936acab58b7a2901df787f71377ffac4809a9b04db7401f689580 AS build
COPY . /build/
WORKDIR /build
RUN make build && mkdir -p /app/.config/gitea-cli

FROM docker.io/chainguard/busybox:latest-glibc@sha256:77beab05a39883aab436c4613b31c29aa68a686be4f4610c14f8f1da75d6bd6b
COPY --from=build /build/gt /bin/gt
COPY --from=build --chown=65532:65532 /app /app
VOLUME [ "/app" ]
ENV HOME="/app"
ENTRYPOINT ["/bin/gt"]
