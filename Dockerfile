# syntax=docker/dockerfile:1

# Build on the runner's own platform and cross-compile: no emulation needed.
FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS TARGETARCH
ARG VERSION=dev COMMIT=none DATE=unknown
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath \
    -ldflags "-s -w \
      -X github.com/enthonysclafani/automatizzatore/internal/version.Version=${VERSION} \
      -X github.com/enthonysclafani/automatizzatore/internal/version.Commit=${COMMIT} \
      -X github.com/enthonysclafani/automatizzatore/internal/version.Date=${DATE}" \
    -o /out/pendolo ./cmd/pendolo

# The runtime image carries the tools shell steps commonly need.
FROM alpine:3.22
RUN apk add --no-cache bash ca-certificates curl jq openssh-client openssl tzdata \
 && adduser -D -H -u 10001 pendolo \
 && mkdir -p /data /automations \
 && chown pendolo:pendolo /data
COPY --from=build /out/pendolo /usr/local/bin/pendolo
ENV PENDOLO_DATA_DIR=/data \
    PENDOLO_AUTOMATIONS_DIR=/automations
USER pendolo
WORKDIR /data
VOLUME ["/data"]
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 CMD ["pendolo", "healthcheck"]
ENTRYPOINT ["pendolo"]
CMD ["server"]
