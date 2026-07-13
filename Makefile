.PHONY: build test vet run docker-build compose-up

# Real file target, not .PHONY: only runs its recipe when config.yaml is
# actually missing (e.g. right after a fresh clone), not on every build.
config.yaml:
	cp config.example.yaml config.yaml

build:
	go build -o bin/gateway ./cmd/gateway

test:
	go test ./... -race -cover

vet:
	go vet ./...

run: config.yaml
	go run ./cmd/gateway

docker-build:
	docker build -t gateway .

compose-up:
	docker compose up --build
