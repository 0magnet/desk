.DEFAULT_GOAL := help
.PHONY: help format tidy lint vet test check install-linters docs

# The targets that matter are `format` and `check`, and they mean the same
# thing here as in 0pcom/skywire, which is the reference for these repos.
# .golangci.yml is copied from there.

PROJECT_BASE := github.com/0magnet/desk
OPTS ?= GO111MODULE=on

help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-18s\033[0m %s\n", $$1, $$2}'

tidy: ## Tidy dependencies
	${OPTS} go mod tidy -v

format: tidy ## Format the code. Needs goimports (make install-linters)
	@if grep -qE '^(replace|exclude)' go.mod; then \
		echo "ERROR: go.mod contains replace or exclude directives which break go install @version"; \
		grep -E '^(replace|exclude)' go.mod; \
		exit 1; \
	fi
	${OPTS} goimports -w -local ${PROJECT_BASE} $(shell go list -f '{{.Dir}}' ./... 2>/dev/null | grep -v /vendor/)

lint: ## Run golangci-lint. Needs it installed (make install-linters)
	command -v golangci-lint || go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	golangci-lint --version
	CGO_ENABLED=0 ${OPTS} golangci-lint run -c .golangci.yml ./...

vet: ## Run go vet
	CGO_ENABLED=0 ${OPTS} go vet ./...

test: ## Run tests
	${OPTS} go test ./...

check: lint vet test ## Run linters, vet and tests

install-linters: ## Install the linters
	${OPTS} go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
	${OPTS} go install golang.org/x/tools/cmd/goimports@latest

docs: ## Regenerate the dependency graph and code counts in the README
	./gendocs.sh
