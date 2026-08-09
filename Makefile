.PHONY: build test lint run demo

build:
	go build -trimpath -o bin/coldshelf ./cmd/coldshelf

test:
	go test ./...

lint:
	go vet ./...

run:
	go run ./cmd/coldshelf serve

demo:
	go run ./cmd/coldshelf demo --db ./coldshelf-demo.db
