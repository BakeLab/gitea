DIST := dist
GO ?= go

export PATH := $(shell $(GO) env GOPATH)/bin:$(PATH)

GOFILES := $(shell find . -name "*.go" -type f)

# Tool packages with pinned versions
GOFUMPT_PACKAGE ?= mvdan.cc/gofumpt@v0.12.0 # renovate: datasource=go
GOLANGCI_LINT_PACKAGE ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 # renovate: datasource=go
DEADCODE_PACKAGE ?= golang.org/x/tools/cmd/deadcode@v0.52.0 # renovate: datasource=go

BUILD_VERSION ?= $(shell git describe --tags --match 'v*' --always | sed 's/-/+/' | sed 's/^v//')
BUILD_VERSION_TAG ?= $(subst +,_,$(BUILD_VERSION))

TAGS ?=
LDFLAGS := -X "gitea.dev/cli/internal/version.Version=$(BUILD_VERSION)" -X "gitea.dev/cli/internal/version.Tags=$(TAGS)" -s -w

# override to allow passing additional goflags via make CLI
override GOFLAGS := $(GOFLAGS) -tags '$(TAGS)' -ldflags '$(LDFLAGS)'

PACKAGES ?= $(shell $(GO) list ./...)
UNIT_PACKAGES ?= $(PACKAGES)
SOURCES ?= $(shell find . -name "*.go" -type f)

# OS specific vars.
ifeq ($(OS), Windows_NT)
	EXECUTABLE := gt.exe
	VET_TOOL := gitea-vet.exe
else
	EXECUTABLE := gt
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
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run

.PHONY: deadcode
deadcode:
	@out=$$($(GO) run $(DEADCODE_PACKAGE) ./cmd/gt ./tools/cli-docs); \
	if [ -n "$$out" ]; then \
		echo "These functions are never called; remove them:"; \
		echo "$${out}"; \
		exit 1; \
	fi

.PHONY: lint-fix
lint-fix:
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run --fix

.PHONY: lint-pr-title
lint-pr-title: ## lint PR title against Conventional Commits (set PR_TITLE=...)
	@node ./tools/lint-pr-title.ts

.PHONY: fmt-check
fmt-check:
	# get all go files and run gofumpt on them
	@diff=$$($(GO) run $(GOFUMPT_PACKAGE) -d $(GOFILES)); \
	if [ -n "$$diff" ]; then \
		echo "Please run 'make fmt' and commit the result:"; \
		echo "$${diff}"; \
		exit 1; \
	fi;

# Man pages are dated by the last commit unless SOURCE_DATE_EPOCH is already set.
.PHONY: docs
docs:
	SOURCE_DATE_EPOCH=$${SOURCE_DATE_EPOCH:-$$(git log -1 --format=%ct 2>/dev/null)} TZ=UTC $(GO) run ./tools/cli-docs --out docs/generated

# Versioned docs archive for release uploads (references, man pages, completions).
# Keep the artifact outside dist/: goreleaser requires dist/ empty after before hooks.
# The .tar.xz suffix matches existing release/publisher/checksum ./**.xz globs.
DOCS_ARCHIVE ?= gitea-cli-$(BUILD_VERSION_TAG)-docs.tar.xz

.PHONY: docs-archive
docs-archive: docs
	@rm -f "$(DOCS_ARCHIVE)" "$(DOCS_ARCHIVE).sha256"
	tar -C docs/generated -c README.md reference man completion | xz -9 > "$(DOCS_ARCHIVE)"
	@bash .goreleaser.checksum.sh "$(DOCS_ARCHIVE)"

# Install generated man pages and shell completions for packagers / local installs.
# Binary install remains `make install` (GOPATH/bin). Override PREFIX/DESTDIR as needed.
PREFIX ?= /usr/local
DESTDIR ?=

.PHONY: install-docs
install-docs: docs
	install -d "$(DESTDIR)$(PREFIX)/share/man/man1"
	install -m 644 docs/generated/man/*.1 "$(DESTDIR)$(PREFIX)/share/man/man1/"
	install -d "$(DESTDIR)$(PREFIX)/share/bash-completion/completions"
	install -m 644 docs/generated/completion/gt.bash "$(DESTDIR)$(PREFIX)/share/bash-completion/completions/gt"
	install -d "$(DESTDIR)$(PREFIX)/share/zsh/site-functions"
	install -m 644 docs/generated/completion/_gt "$(DESTDIR)$(PREFIX)/share/zsh/site-functions/_gt"
	install -d "$(DESTDIR)$(PREFIX)/share/fish/vendor_completions.d"
	install -m 644 docs/generated/completion/gt.fish "$(DESTDIR)$(PREFIX)/share/fish/vendor_completions.d/gt.fish"
	install -d "$(DESTDIR)$(PREFIX)/share/powershell/Modules/gt"
	install -m 644 docs/generated/completion/gt.ps1 "$(DESTDIR)$(PREFIX)/share/powershell/Modules/gt/gt.ps1"

.PHONY: unit-test
unit-test:
	$(GO) test $(UNIT_PACKAGES)

.PHONY: integration-test
integration-test:
	@test -n "$$GITEA_CLI_TEST_URL" || { echo 'Set GITEA_CLI_TEST_URL to a disposable Gitea instance' >&2; exit 1; }
	@test -r "$$GITEA_CLI_TEST_TOKEN_FILE" || { echo 'Set GITEA_CLI_TEST_TOKEN_FILE to a readable administrator token file' >&2; exit 1; }
	@test -r "$$GITEA_CLI_TEST_REVIEWER_TOKEN_FILE" || { echo 'Set GITEA_CLI_TEST_REVIEWER_TOKEN_FILE to a readable reviewer token file' >&2; exit 1; }
	$(GO) test -count=1 -race -run '^TestGitea' $(PACKAGES)

.PHONY: test
test: unit-test

.PHONY: unit-test-coverage
unit-test-coverage:
	$(GO) test -race -cover -coverprofile coverage.out $(UNIT_PACKAGES) && echo "\n==>\033[32m Ok\033[m\n" || exit 1

.PHONY: tidy-check
tidy-check:
	@$(GO) mod tidy -diff || { echo "Please run 'make tidy' and commit the result"; exit 1; }

.PHONY: tidy
tidy:
	$(eval GO_TOOLCHAIN := $(shell grep -Eo '^toolchain\s+go[0-9.]+' go.mod | cut -d' ' -f2))
	$(GO) mod tidy
	@# workaround https://github.com/golang/go/issues/75331: restore toolchain if tidy dropped it
	@if [ -n "$(GO_TOOLCHAIN)" ] && ! grep -qE '^toolchain\s' go.mod; then \
		$(GO) mod edit -toolchain=$(GO_TOOLCHAIN); \
	fi

.PHONY: check
check: test

.PHONY: install
install: $(SOURCES)
	@echo "installing to $(shell $(GO) env GOPATH)/bin/$(EXECUTABLE)"
	$(GO) install -v $(BUILDMODE) $(GOFLAGS) ./cmd/gt

.PHONY: build
build: $(EXECUTABLE)

$(EXECUTABLE): $(SOURCES)
	$(GO) build $(BUILDMODE) $(GOFLAGS) -o $@ ./cmd/gt

.PHONY: build-image
build-image:
	docker build -t gitea/gitea-cli:$(BUILD_VERSION_TAG) .
