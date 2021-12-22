UNITTEST?=$$(go list ./... |grep -v 'vendor')

ifeq ($(GOPATH),)
	GOPATH:=$(shell go env GOPATH)
endif

.PHONY: test
test: fmtcheck
	go test -tags "all" $(UNITTEST) || exit 1
	echo $(UNITTEST) | \
    		xargs -t -n4 go test -tags "all" $(TESTARGS) -timeout=60s -parallel=4

.PHONY: lint
lint:
	@if command -v golangci-lint; then (golangci-lint run ./...); else echo "golangci-lint not found"; exit 1; fi

.PHONY: fmt
fmt:
	find . -name '*.go' | grep -v vendor | xargs gofmt -s -w

.PHONY: fmtcheck
fmtcheck:
	@gofmt_files=$$(find . -name '*.go' | grep -v vendor | xargs gofmt -l) ; \
	if [ -n "$${gofmt_files}" ]; then \
		echo 'gofmt needs running on the following files:'; \
		echo "$${gofmt_files}" ; \
		echo "You can use the command: \`make fmt\` to reformat code." ; \
		exit 1 ;\
	fi
