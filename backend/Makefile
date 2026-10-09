.PHONY: build test check release

build:
	CGO_ENABLED=0 go build -trimpath -o dist/ljmdb ./cmd/ljmdb

test:
	go test ./...

check:
	go vet ./...
	go test -race ./...

release:
	bash scripts/build.sh
