# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.25

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath \
    -ldflags="-w -s -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
    -o /out/docker-state-exporter ./cmd/docker-state-exporter

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /out/docker-state-exporter /docker-state-exporter
USER nonroot:nonroot
EXPOSE 8080
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD ["/docker-state-exporter", "-healthcheck"]
ENTRYPOINT ["/docker-state-exporter"]
CMD ["-listen-address=:8080"]

LABEL org.opencontainers.image.title="docker-state-exporter" \
      org.opencontainers.image.description="Prometheus exporter for Docker container state." \
      org.opencontainers.image.source="https://github.com/dblencowe/docker-state-exporter" \
      org.opencontainers.image.licenses="MIT"
