# One image per program: docker build --build-arg PROGRAM=simple-sc -f deploy/docker/Dockerfile .
# Multi-architecture: docker buildx build --platform linux/amd64,linux/arm64 ...
# All state lives in /data (mount a volume there). The image contains no schema files,
# put them into /data/schemas or set SSCAC_SCHEMA_DIR.

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG PROGRAM=simple-sc
ARG VERSION=dev
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w -X github.com/michaburger/simple-sc-ac/internal/common/buildinfo.Version=$VERSION" \
    -o /out/app ./cmd/$PROGRAM

# Runs as root on purpose: volumes on balena and fly.io are owned by root, a non-root
# user could not write /data there. The image has no shell and no package manager.
FROM gcr.io/distroless/static-debian12
COPY --from=build /out/app /app
ENV SSCAC_DATA_DIR=/data
VOLUME /data
# BSSCI and SCACI (simple-sc), ignored by simple-ac
EXPOSE 16107 17107
ENTRYPOINT ["/app"]
