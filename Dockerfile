# build stage
FROM golang:1.24-alpine AS builder
RUN apk add --no-cache

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

ENV GOCACHE=/root/.cache/go-build
RUN --mount=type=cache,target="/root/.cache/go-build" go build -o /build/app .

# run stage
FROM alpine:3.21
LABEL maintainer="oatsaysai <oat.saysai@gmail.com>"

RUN apk add --no-cache \
    bash \
    tzdata \
    nodejs \
    npm && \
    npm install -g firebase-tools --unsafe-perm && \
    rm -rf /var/cache/apk/* /tmp/* /root/.npm /usr/lib/node_modules/npm/docs /usr/lib/node_modules/npm/html /usr/lib/node_modules/npm/man /usr/share/man

ENV TZ="Asia/Bangkok"
ENV PATH=/usr/local/bin:/usr/bin:/bin:/sbin:/usr/lib/node_modules/.bin:$PATH

# Create the /app directory
RUN mkdir -p /app
WORKDIR /app

# Copy the application binary
COPY --from=builder /build/app ./app

ENTRYPOINT [ "./app" ]