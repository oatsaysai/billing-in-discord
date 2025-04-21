run:
	go run ./

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
		--name billing-bot \
		-v $(PWD)/config.yaml:/config.yaml \
		image-registry.fintblock.com/billing-bot
