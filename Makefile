.PHONY: preflight build test lint fmt vet clean install py-test spec spec-check

# preflight is the CI gate: gofmt + vet + build + test + python guard tests + spec drift
preflight: fmt-check vet build test py-test spec-check

build:
	go build ./...

test:
	go test -race -coverprofile=cover.out ./...
	@go tool cover -func=cover.out | grep '^github.com/anatolykoptev/go-hooppy/' | awk '{sum+=$$3; n++} END {if (n>0) printf "core coverage: %.1f%%\n", sum/n}'
	@core=$$(go tool cover -func=cover.out | grep '^github.com/anatolykoptev/go-hooppy/' | awk '{sum+=$$3; n++} END {if (n>0) printf "%.1f", sum/n}'); \
	if awk "BEGIN{exit !($$core < 65.0)}"; then \
		echo "FAIL: core coverage $$core%% < 65%% (ratchet floor; raise only, never lower)"; \
		exit 1; \
	fi
# Coverage floor is a ratchet, not a magic constant. Metric = mean of per-function
# coverage percentages over lines matching '^github.com/anatolykoptev/go-hooppy/'
# (core package + cmd). Measured 2026-07-29: 68.39% across 214 functions. Floor
# 65.0% leaves ~3.4 points of headroom so ordinary churn (a few new untested
# functions) does not trip it, while a real collapse (a quarter of the codebase
# losing coverage) falls through immediately. When actual coverage durably moves
# up, bump the floor to just below the new measured value; NEVER lower it.

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

# py-test runs the issue-open-tasks guard tests (stdlib only, no pip install).
# Part of preflight so the guard is exercised on every PR alongside the Go gate.
py-test:
	python3 scripts/test_issue_open_tasks.py

# spec regenerates api/openapi-measured.yaml from the recorded fixtures.
spec:
	go run ./cmd/specgen

# spec-check fails when the committed spec is not what the generator produces.
# It is in preflight because the conformance test cannot catch this: that test
# validates each fixture against its schema, so editing the hand-maintained
# endpoint table and forgetting to regenerate leaves every fixture still valid
# and the suite still green while the spec no longer describes the generator's
# output. Verified by mutating the table and watching go test ./api/... stay ok
# while this target fails.
spec-check:
	go run ./cmd/specgen -check
