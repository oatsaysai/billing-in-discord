.PHONY: run build start-db stop-db build-docker run-docker clean

run:
	go run ./cmd/server

build:
	go build -o bin/billing-discord ./cmd/server

install:
	go install ./cmd/server

start-db:
	./tools/start_db.sh
	sleep 3
	./tools/drop_and_create_db.sh

stop-db:
	./tools/stop_db.sh

build-docker:
	docker build -t image-registry.fintblock.com/billing-bot .

run-docker:
	docker run \
		-d \
		-e TZ=Asia/Bangkok \
		--name billing-bot \
		-v $(PWD)/config.yaml:/config.yaml \
		image-registry.fintblock.com/billing-bot

clean:
	rm -rf bin/
	go clean