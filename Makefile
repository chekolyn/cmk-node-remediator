.PHONY: build test lint vendor docker-build docker-push clean

BINARY_NAME=cmk-node-remediation
IMAGE_TAG?=latest
IMAGE_REPO=ghcr.io/crusoecloud/cmk-node-remediation

build:
	CGO_ENABLED=0 go build -o $(BINARY_NAME) ./cmd/manager

test:
	go test -v -race ./...

lint:
	go vet ./...

docker-build:
	docker build -t $(IMAGE_REPO):$(IMAGE_TAG) .

docker-push:
	docker push $(IMAGE_REPO):$(IMAGE_TAG)

clean:
	rm -f $(BINARY_NAME)
