# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26

FROM --platform=$BUILDPLATFORM golang:${GO_VERSION} AS builder
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH:-amd64} \
    go build -trimpath -ldflags="-s -w" -o /out/tercios ./cmd/tercios

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/tercios /usr/local/bin/tercios

ENTRYPOINT ["/usr/local/bin/tercios"]
