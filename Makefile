.PHONY: build test lint vendor docker-build docker-push clean

BINARY_NAME=node-remediation
IMAGE_TAG?=latest
IMAGE_REPO=ghcr.io/crusoecloud/node-remediation

build:
	CGO_ENABLED=0 go build -mod=vendor -o $(BINARY_NAME) ./cmd/manager

test:
	go test -v -race -mod=vendor ./...

lint:
	go vet -mod=vendor ./...

vendor:
	go mod vendor

docker-build:
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

docker-push:
	docker push $(IMAGE_REPO):$(IMAGE_TAG)

clean:
	rm -f $(BINARY_NAME)
