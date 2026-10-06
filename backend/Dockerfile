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
  && mkdir -p /data \
  && chown -R noxbackend:noxbackend /data

EXPOSE 8080

COPY --from=build /out/nox-backend /usr/local/bin/nox-backend
RUN ln -s /usr/local/bin/nox-backend /usr/local/bin/nox-sync

USER noxbackend

ENTRYPOINT ["/usr/local/bin/nox-backend"]
