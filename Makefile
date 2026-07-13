.PHONY: build test vet run docker-build compose-up

build:
	go build -o bin/gateway ./cmd/gateway

test:
	go test ./... -race -cover

vet:
	go vet ./...

run:
	go run ./cmd/gateway

docker-build:
	docker build -t gateway .

compose-up:
	docker compose up --build
