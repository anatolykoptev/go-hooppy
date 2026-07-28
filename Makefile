.PHONY: preflight build test lint fmt vet clean install

# preflight is the CI gate: gofmt + vet + build + test
preflight: fmt-check vet build test

build:
	go build ./...

test:
	go test -race -coverprofile=cover.out ./...
	@go tool cover -func=cover.out | grep '^github.com/anatolykoptev/go-hooppy/' | awk '{sum+=$$3; n++} END {if (n>0) printf "core coverage: %.1f%%\n", sum/n}'
	@core=$$(go tool cover -func=cover.out | grep '^github.com/anatolykoptev/go-hooppy/' | awk '{sum+=$$3; n++} END {if (n>0) printf "%.1f", sum/n}'); \
	if awk "BEGIN{exit !($$core < 40.0)}"; then \
		echo "FAIL: core coverage $$core%% < 40%%"; \
		exit 1; \
	fi

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
