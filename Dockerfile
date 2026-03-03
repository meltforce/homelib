FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /homelib .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates openssh-client

ARG VERSION=dev
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.source="https://github.com/meltforce/homelib"
LABEL org.opencontainers.image.description="Homelab inventory collector"

COPY --from=builder /homelib /usr/local/bin/homelib

VOLUME /data
EXPOSE 443

ENTRYPOINT ["homelib"]
CMD ["--config", "/data/config.yaml"]
