.PHONY: build test vet lint clean

build:
	go build -o nosy ./cmd/nosy

test:
	go test ./...

vet:
	go vet ./...

check: vet build test

clean:
	rm -f nosy
