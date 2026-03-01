FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /homelib .

FROM alpine:3.21
RUN apk add --no-cache ca-certificates openssh-client
COPY --from=builder /homelib /usr/local/bin/homelib

VOLUME /data
EXPOSE 443

ENTRYPOINT ["homelib"]
CMD ["--config", "/data/config.yaml"]
