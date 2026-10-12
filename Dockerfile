FROM docker.io/chainguard/go:latest AS build
COPY . /build/
WORKDIR /build
RUN make build && mkdir -p /app/.config/gitea-cli

FROM docker.io/chainguard/busybox:latest-glibc
COPY --from=build /build/gt /bin/gt
COPY --from=build --chown=65532:65532 /app /app
VOLUME [ "/app" ]
ENV HOME="/app"
ENTRYPOINT ["/bin/sh", "-c"]
CMD [ "gt" ]
