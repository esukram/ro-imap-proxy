BINARY = imap-proxy

.PHONY: all build test vet clean

all: clean build vet test

build:
	go build -o $(BINARY) ./cmd/imap-proxy/

test:
	go test ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
