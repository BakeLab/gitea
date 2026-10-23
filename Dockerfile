FROM docker.io/chainguard/go:latest@sha256:b60616beec1209c409a2b5cdd3d3a045f4ae0eb7eae3858bfd774a4b58f83d18 AS build
COPY . /build/
WORKDIR /build
RUN make build && mkdir -p /app/.config/gitea-cli

FROM docker.io/chainguard/busybox:latest-glibc@sha256:98e9e7306c899a8af252ef1ff50c54def91c2fe04677298eafdf0b902f2980ed
COPY --from=build /build/gt /bin/gt
COPY --from=build --chown=65532:65532 /app /app
VOLUME [ "/app" ]
ENV HOME="/app"
ENTRYPOINT ["/bin/gt"]
