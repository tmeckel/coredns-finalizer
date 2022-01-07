GOFILES := $(shell find . -name "*.go" -type f)
UNITTEST?=$$(go list ./... |grep -v 'vendor')
GOFMT ?= gofumpt -l -extra

ifeq ($(GOPATH),)
	GOPATH:=$(shell go env GOPATH)
endif

.PHONY: tools
tools:
	@echo "==> Ensure required tools are installed..."
	@hash gofumpt > /dev/null 2>&1; if [ $$? -ne 0 ]; then \
		echo "==>  Install gofumpt..."; \
		go install mvdan.cc/gofumpt@latest > /dev/null; \
	fi
	@hash misspell > /dev/null 2>&1; if [ $$? -ne 0 ]; then \
		echo "==>  Install misspell..."; \
		go install github.com/client9/misspell/cmd/misspell@latest; \
	fi
	@hash golangci-lint > /dev/null 2>&1; if [ $$? -ne 0 ]; then \
		echo "==>  Install golangci-lint..."; \
		curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$(GOPATH)/bin"; \
	fi

.PHONY: test
test:
	@echo "==> Executing unit tests..."
	@go test -tags "all" $(UNITTEST) || exit 1
	@echo $(UNITTEST) | \
    		xargs -t -n4 go test -tags "all" $(TESTARGS) -timeout=60s -parallel=4

.PHONY: lint
lint: tools
	@echo "==> Linting source code with golangci-lint..."
	@if command -v golangci-lint > /dev/null; then (golangci-lint run ./...); else echo "golangci-lint not found"; exit 1; fi

.PHONY: fmt
fmt: tools
	@echo "==> Fixing source code with gofumpt..."
	$(GOFMT) -w $(GOFILES)

.PHONY: fmtcheck
fmt-check: tools
	@echo "==> Checking source code format with gofumpt..."
	@diff=$$($(GOFMT) -d $(GOFILES)); \
	if [ -n "$$diff" ]; then \
		echo "Please run 'make fmt' and commit the result:"; \
		echo "$${diff}"; \
		exit 1; \
	fi;

.PHONY: vet
vet:
	@echo "==> Validating code with vet..."
	@go vet $$(go list ./... | grep -v vendor/) ; if [ $$? -eq 1 ]; then \
		echo ""; \
		echo "Vet found suspicious constructs. Please check the reported constructs"; \
		echo "and fix them if necessary."; \
		exit 1; \
	fi

.PHONY: misspell-check
misspell-check: tools
	@echo "==> Check spelling of code with misspell..."
	@misspell -error $(GOFILES)

.PHONY: misspell
misspell: tools
	@echo "==> Fix spelling errors in code with misspell..."
	@misspell -w $(GOFILES)

.PHONY: ci
ci: fmt-check misspell-check lint vet test
