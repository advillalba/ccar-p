.PHONY: fmt test vet web-install web-check build

fmt:
	gofmt -w cmd internal

vet:
	go vet ./...

test:
	go test ./...

web-install:
	npm --prefix web ci

web-check:
	npm --prefix web run check

build:
	go build ./cmd/... && npm --prefix web run build
