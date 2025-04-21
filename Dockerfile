# build stage
FROM golang:1.23-alpine3.21 AS builder
RUN apk add --no-cache

WORKDIR /app

ADD go.mod go.sum /app/
RUN go mod download

COPY *.go /app
RUN go build -o /build/app

# run stage
FROM alpine:3.21
LABEL maintainer="oatsaysai <oat.saysai@gmail.com>"

RUN apk add --no-cache \
    bash \
    tzdata && \
    rm -rf /var/cache/apk

ENV TZ="Asia/Bangkok"

COPY --from=builder /build/app /app

ENTRYPOINT [ "/app" ]
