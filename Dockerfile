# syntax=docker/dockerfile:1
# Multi-arch build: docker buildx build --platform linux/amd64,linux/arm64,linux/arm/v7,linux/arm/v6 .
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS TARGETARCH TARGETVARIANT
ARG VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH GOARM=${TARGETVARIANT#v} \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/opencouchpad ./cmd/opencouchpad

FROM scratch
COPY --from=build /out/opencouchpad /opencouchpad
# Needs: --network host (mDNS + LAN access) and --device /dev/uinput.
# Optional: -v /etc/default/keyboard:/etc/default/keyboard:ro for layout auto-detect.
EXPOSE 80
ENTRYPOINT ["/opencouchpad"]
