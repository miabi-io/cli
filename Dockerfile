# syntax=docker/dockerfile:1

# Built on the runner's own platform and cross-compiled, so no target compiles Go under QEMU.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG TARGETOS TARGETARCH TARGETVARIANT
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} go build -trimpath \
    -ldflags "-s -w -X github.com/miabi-io/cli/internal/cmd.version=${VERSION}" \
    -o /miabi .

FROM alpine:3.20
# Re-declare in this stage so ${VERSION} is in scope for the label below.
ARG VERSION=dev
LABEL org.opencontainers.image.title="Miabi CLI" \
      org.opencontainers.image.description="Imperative client for a Miabi control panel: drive the deploy flow (and declarative apply/delete) from a terminal or CI against the public /api/v1 HTTP API." \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.authors="Jonas Kaninda" \
      org.opencontainers.image.vendor="miabi-io" \
      org.opencontainers.image.url="https://github.com/miabi-io/cli" \
      org.opencontainers.image.source="https://github.com/miabi-io/cli" \
      org.opencontainers.image.documentation="https://github.com/miabi-io/cli#readme" \
      org.opencontainers.image.licenses="AGPL-3.0-or-later"
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 miabi
COPY --from=build /miabi /usr/local/bin/miabi
USER miabi
ENTRYPOINT ["miabi"]
