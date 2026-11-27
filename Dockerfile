# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.4-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod *.go ./
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -buildid=" -o /ipv6-prefix-sync .

FROM scratch
COPY --from=build /ipv6-prefix-sync /ipv6-prefix-sync
USER 65534:65534
ENTRYPOINT ["/ipv6-prefix-sync"]
