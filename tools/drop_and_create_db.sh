#!/bin/sh

DB_NAME="billing-db"
CONTAINER_NAME="postgres"

docker exec -i "$CONTAINER_NAME" psql -U postgres -c "DROP DATABASE IF EXISTS \"$DB_NAME\";" &&
docker exec -i "$CONTAINER_NAME" psql -U postgres -c "CREATE DATABASE \"$DB_NAME\";"