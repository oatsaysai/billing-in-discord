# build stage
FROM golang:1.23-alpine3.19 AS builder
RUN apk add --no-cache

WORKDIR /app

ADD go.mod go.sum /app/
RUN go mod download

COPY main.go /app
RUN go build -o /build/app

# run stage
FROM alpine:3.19
LABEL maintainer="oatsaysai <oat.saysai@gmail.com>"
ENV TZ="Asia/Bangkok"

COPY --from=builder /build/app /app

ENTRYPOINT [ "/app" ]
