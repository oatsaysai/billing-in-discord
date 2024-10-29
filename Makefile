build-docker:
	docker build -t image-registry.fintblock.com/billing-bot .

run-docker:
	docker run \
		-d \
		--name billing-bot \
		-v $(PWD)/config.yaml:/config.yaml \
		image-registry.fintblock.com/billing-bot
