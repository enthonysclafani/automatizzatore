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

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/pendolo /usr/local/bin/pendolo
USER nonroot:nonroot
WORKDIR /automations
ENTRYPOINT ["/usr/local/bin/pendolo"]
CMD ["help"]
