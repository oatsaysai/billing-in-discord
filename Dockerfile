# build stage
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache

WORKDIR /app

ADD go.mod go.sum /app/
RUN go mod download

COPY *.go /app

ENV GOCACHE=/root/.cache/go-build
RUN --mount=type=cache,target="/root/.cache/go-build" go build -o /build/app

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
