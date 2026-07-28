.PHONY: preflight build test lint fmt vet clean install

# preflight is the CI gate: gofmt + vet + build + test
preflight: fmt-check vet build test

build:
	go build ./...

test:
	go test ./...

vet:
	go vet ./...

fmt:
	@go fmt ./...

fmt-check:
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "gofmt issues in:"; echo "$$unformatted"; \
		exit 1; \
	fi

clean:
	rm -rf bin/

install: build
	go install ./cmd/hooppy
	go install ./cmd/hooppy-mcp
