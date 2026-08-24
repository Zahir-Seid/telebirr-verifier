.PHONY: build test test-race cover vet fmt lint tidy clean

build:
	go build ./...

test:
	go test ./...

test-race:
	go test -race -count=1 ./...

cover:
	go test -race -covermode=atomic -coverprofile=coverage.txt ./...
	go tool cover -func=coverage.txt

vet:
	go vet ./...

fmt:
	gofmt -s -w .

lint:
	golangci-lint run

tidy:
	go mod tidy

clean:
	rm -f coverage.txt coverage.html
