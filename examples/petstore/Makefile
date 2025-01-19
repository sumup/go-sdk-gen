.PHONY: build
.DEFAULT_GOAL := build
build:
	go-sdk-gen generate --mod github.com/sumup/go-sdk-gen/examples/petstore --pkg petstore --name 'Petstore' ./openapi.json
