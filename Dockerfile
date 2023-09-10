FROM cgr.dev/chainguard/go:latest AS build
COPY . /build/
WORKDIR /build
RUN	make build

FROM cgr.dev/chainguard/static:latest
COPY --from=build /build/tea /tea
VOLUME [ "/app" ]
ENV HOME="/app"
ENTRYPOINT ["/tea"]
