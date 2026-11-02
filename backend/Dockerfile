# syntax=docker/dockerfile:1

FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal

ARG VERSION=dev
RUN go build -ldflags "-X main.buildVersion=${VERSION}" -o /out/nox-backend ./cmd/nox-backend

FROM alpine:3.23

RUN adduser -D -H -u 10001 noxbackend \
  && mkdir -p /data /run/nox-suite \
  && chown -R noxbackend:noxbackend /data /run/nox-suite \
  && chmod 3770 /run/nox-suite

ENV NOX_BACKEND_SUITE_SOCKET_DIR=/run/nox-suite

EXPOSE 8080

COPY --from=build /out/nox-backend /usr/local/bin/nox-backend

USER noxbackend

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD ["/usr/local/bin/nox-backend", "healthcheck"]

ENTRYPOINT ["/usr/local/bin/nox-backend"]
