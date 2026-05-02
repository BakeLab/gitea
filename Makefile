DIST := dist
GO ?= go
SHASUM ?= shasum -a 256

export PATH := $($(GO) env GOPATH)/bin:$(PATH)

GOFILES := $(shell find . -name "*.go" -type f ! -path "*/bindata.go")

# Tool packages with pinned versions
GOFUMPT_PACKAGE ?= mvdan.cc/gofumpt@v0.9.2
GOLANGCI_LINT_PACKAGE ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4

ifneq ($(DRONE_TAG),)
	VERSION ?= $(subst v,,$(DRONE_TAG))
	TEA_VERSION ?= $(VERSION)
else
	ifneq ($(DRONE_BRANCH),)
		VERSION ?= $(subst release/v,,$(DRONE_BRANCH))
	else
		VERSION ?= main
	endif
	TEA_VERSION ?= $(shell git describe --tags --always | sed 's/-/+/' | sed 's/^v//')
endif
TEA_VERSION_TAG ?= $(shell sed 's/+/_/' <<< $(TEA_VERSION))

TAGS ?=
SDK ?= $(shell $(GO) list -f '{{.Version}}' -m code.gitea.io/sdk/gitea)
LDFLAGS := -X "code.gitea.io/tea/modules/version.Version=$(TEA_VERSION)" -X "code.gitea.io/tea/modules/version.Tags=$(TAGS)" -X "code.gitea.io/tea/modules/version.SDK=$(SDK)" -s -w

# override to allow passing additional goflags via make CLI
override GOFLAGS := $(GOFLAGS) -tags '$(TAGS)' -ldflags '$(LDFLAGS)'

PACKAGES ?= $(shell $(GO) list ./... | grep -v '^code.gitea.io/tea/tests')
UNIT_PACKAGES ?= $(PACKAGES)
INTEGRATION_PACKAGES ?= $(shell $(GO) list ./tests/... 2>/dev/null)
INTEGRATION_TEST_TAGS ?= testtools
SOURCES ?= $(shell find . -name "*.go" -type f)

# OS specific vars.
ifeq ($(OS), Windows_NT)
	EXECUTABLE := tea.exe
	VET_TOOL := gitea-vet.exe
else
	EXECUTABLE := tea
	VET_TOOL := gitea-vet
endif

.PHONY: all
all: build

.PHONY: clean
clean:
	$(GO) clean -i ./...
	rm -rf $(EXECUTABLE) $(DIST)

.PHONY: fmt
fmt:
	$(GO) run $(GOFUMPT_PACKAGE) -w $(GOFILES)

.PHONY: vet
vet:
	# Default vet
	$(GO) vet $(PACKAGES)
	# Custom vet
	$(GO) build code.gitea.io/gitea-vet
	$(GO) vet -vettool=$(VET_TOOL) $(PACKAGES)

.PHONY: lint
lint:
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run --build-tags testtools

.PHONY: lint-fix
lint-fix:
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run --build-tags testtools --fix

.PHONY: fmt-check
fmt-check:
	# get all go files and run gofumpt on them
	@diff=$$($(GO) run $(GOFUMPT_PACKAGE) -d $(GOFILES)); \
	if [ -n "$$diff" ]; then \
		echo "Please run 'make fmt' and commit the result:"; \
		echo "$${diff}"; \
		exit 1; \
	fi;

.PHONY: docs
docs:
	$(GO) run docs/docs.go --out docs/CLI.md

.PHONY: docs-check
docs-check:
	@DIFF=$$($(GO) run docs/docs.go | diff docs/CLI.md -); \
	if [ -n "$$DIFF" ]; then \
		echo "Please run 'make docs' and commit the result:"; \
		echo "$$DIFF"; \
		exit 1; \
	fi;

.PHONY: unit-test
unit-test:
	$(GO) test $(UNIT_PACKAGES)

.PHONY: integration-test
integration-test:
	@if [ -n "$(INTEGRATION_PACKAGES)" ]; then \
		$(GO) test -tags='$(INTEGRATION_TEST_TAGS)' $(INTEGRATION_PACKAGES); \
	else \
		echo "No integration test packages found"; \
	fi

.PHONY: test
test: unit-test integration-test

.PHONY: unit-test-coverage
unit-test-coverage:
	$(GO) test -cover -coverprofile coverage.out $(UNIT_PACKAGES) && echo "\n==>\033[32m Ok\033[m\n" || exit 1

.PHONY: tidy
tidy:
	$(GO) mod tidy

.PHONY: check
check: test

.PHONY: install
install: $(SOURCES)
	@echo "installing to $(shell $(GO) env GOPATH)/bin/$(EXECUTABLE)"
	$(GO) install -v $(BUILDMODE) $(GOFLAGS)

.PHONY: build
build: $(EXECUTABLE)

$(EXECUTABLE): $(SOURCES)
	$(GO) build $(BUILDMODE) $(GOFLAGS) -o $@

.PHONY: build-image
build-image:
	docker build --build-arg VERSION=$(TEA_VERSION) -t gitea/tea:$(TEA_VERSION_TAG) .
