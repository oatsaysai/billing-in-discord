#!/bin/sh

docker run \
    -d \
    -p 5432:5432 \
    -e TZ=Asia/Bangkok \
    -e POSTGRES_PASSWORD=postgres \
    --name postgres postgres:16-alpine