FROM cgr.dev/chainguard/go:latest AS buildenv
COPY . /build/
WORKDIR /build
RUN	make clean build STATIC=true

FROM cgr.dev/chainguard/static:latest
COPY --from=buildenv /build/tea /tea
VOLUME [ "/app" ]
ENV HOME="/app"
ENTRYPOINT ["/tea"]
