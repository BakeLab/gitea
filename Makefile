DIST := dist
export GO111MODULE=on
export CGO_ENABLED=0

GO ?= go
SHASUM ?= shasum -a 256

export PATH := $($(GO) env GOPATH)/bin:$(PATH)

GOFILES := $(shell find . -name "*.go" -type f ! -path "*/bindata.go")
GOFMT ?= gofmt -s

ifneq ($(DRONE_TAG),)
	VERSION ?= $(subst v,,$(DRONE_TAG))
	TEA_VERSION ?= $(VERSION)
else
	ifneq ($(DRONE_BRANCH),)
		VERSION ?= $(subst release/v,,$(DRONE_BRANCH))
	else
		VERSION ?= master
	endif
	TEA_VERSION ?= $(shell git describe --tags --always | sed 's/-/+/' | sed 's/^v//')
endif
TEA_VERSION_TAG ?= $(shell sed 's/+/_/' <<< $(TEA_VERSION))

TAGS ?=
SDK ?= $(shell $(GO) list -f '{{.Version}}' -m code.gitea.io/sdk/gitea)
LDFLAGS := -X "main.Version=$(TEA_VERSION)" -X "main.Tags=$(TAGS)" -X "main.SDK=$(SDK)" -s -w

# override to allow passing additional goflags via make CLI
override GOFLAGS := $(GOFLAGS) -tags '$(TAGS)' -ldflags '$(LDFLAGS)'

PACKAGES ?= $(shell $(GO) list ./...)
SOURCES ?= $(shell find . -name "*.go" -type f)

# OS specific vars.
ifeq ($(OS), Windows_NT)
	EXECUTABLE := tea.exe
	VET_TOOL := gitea-vet.exe
else
	EXECUTABLE := tea
	VET_TOOL := gitea-vet
	ifneq ($(shell uname -s), OpenBSD)
		override BUILDMODE := -buildmode=pie
	endif
endif

.PHONY: all
all: build

.PHONY: clean
clean:
	$(GO) clean -i ./...
	rm -rf $(EXECUTABLE) $(DIST)

.PHONY: fmt
fmt:
	$(GOFMT) -w $(GOFILES)

.PHONY: vet
vet:
	# Default vet
	$(GO) vet $(PACKAGES)
	# Custom vet
	$(GO) build code.gitea.io/gitea-vet
	$(GO) vet -vettool=$(VET_TOOL) $(PACKAGES)

.PHONY: lint
lint: install-lint-tools
	$(GO) run github.com/mgechev/revive@latest -config .revive.toml ./... || exit 1

.PHONY: misspell-check
misspell-check: install-lint-tools
	$(GO) run github.com/client9/misspell/cmd/misspell@latest -error -i unknwon,destory $(GOFILES)

.PHONY: misspell
misspell: install-lint-tools
	$(GO) run github.com/client9/misspell/cmd/misspell@latest -w -i unknwon $(GOFILES)

.PHONY: fmt-check
fmt-check:
	# get all go files and run go fmt on them
	@diff=$$($(GOFMT) -d $(GOFILES)); \
	if [ -n "$$diff" ]; then \
		echo "Please run 'make fmt' and commit the result:"; \
		echo "$${diff}"; \
		exit 1; \
	fi;

.PHONY: docs
docs:
	$(GO) run . docs --out docs/CLI.md

.PHONY: docs-check
docs-check:
	@DIFF=$$($(GO) run . docs | diff docs/CLI.md -); \
	if [ -n "$$DIFF" ]; then \
		echo "Please run 'make docs' and commit the result:"; \
		echo "$$DIFF"; \
		exit 1; \
	fi;

.PHONY: test
test:
	$(GO) test -tags='sqlite sqlite_unlock_notify' $(PACKAGES)

.PHONY: unit-test-coverage
unit-test-coverage:
	$(GO) test -tags='sqlite sqlite_unlock_notify' -cover -coverprofile coverage.out $(PACKAGES) && echo "\n==>\033[32m Ok\033[m\n" || exit 1

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

.PHONY: release
release: release-dirs install-release-tools release-os release-compress release-check

.PHONY: release-dirs
release-dirs:
	mkdir -p $(DIST)/release

.PHONY: release-os
release-os:
	CGO_ENABLED=0 $(GO) run github.com/mitchellh/gox@latest -verbose -cgo=false $(GOFLAGS) -osarch='!darwin/386 !darwin/arm' -os="windows linux darwin" -arch="386 amd64 arm arm64" -output="$(DIST)/release/tea-$(VERSION)-{{.OS}}-{{.Arch}}"

.PHONY: release-compress
release-compress: install-release-tools
	cd $(DIST)/release/; for file in `find . -type f -name "*"`; do echo "compressing $${file}" && $(GO) run github.com/ulikunitz/xz/cmd/gxz@latest -k -9 $${file}; done;

.PHONY: release-check
release-check: install-release-tools
	cd $(DIST)/release/; for file in `find . -type f -name "*"`; do echo "checksumming $${file}" && $(SHASUM) `echo $${file} | sed 's/^..//'` > $${file}.sha256; done;

### tools
install-release-tools:
	@hash gox > /dev/null 2>&1; if [ $$? -ne 0 ]; then \
		$(GO) install github.com/mitchellh/gox@latest; \
	fi
	@hash gxz > /dev/null 2>&1; if [ $$? -ne 0 ]; then \
		$(GO) install github.com/ulikunitz/xz/cmd/gxz@latest; \
	fi

install-lint-tools:
	@hash revive > /dev/null 2>&1; if [ $$? -ne 0 ]; then \
		$(GO) install github.com/mgechev/revive@latest; \
	fi
	@hash misspell > /dev/null 2>&1; if [ $$? -ne 0 ]; then \
		$(GO) install github.com/client9/misspell/cmd/misspell@latest; \
	fi
