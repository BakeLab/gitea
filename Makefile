DIST := dist
GO ?= go
SHASUM ?= shasum -a 256

export PATH := $($(GO) env GOPATH)/bin:$(PATH)

GOFILES := $(shell find . -name "*.go" -type f ! -path "*/bindata.go")

# Tool packages with pinned versions
GOFUMPT_PACKAGE ?= mvdan.cc/gofumpt@v0.12.0
GOLANGCI_LINT_PACKAGE ?= github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

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
LDFLAGS := -X "gitea.dev/tea/internal/version.Version=$(TEA_VERSION)" -X "gitea.dev/tea/internal/version.Tags=$(TAGS)" -s -w

# override to allow passing additional goflags via make CLI
override GOFLAGS := $(GOFLAGS) -tags '$(TAGS)' -ldflags '$(LDFLAGS)'

PACKAGES ?= $(shell $(GO) list ./...)
UNIT_PACKAGES ?= $(PACKAGES)
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
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run

.PHONY: lint-fix
lint-fix:
	$(GO) run $(GOLANGCI_LINT_PACKAGE) run --fix

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
DOCS_ARCHIVE ?= tea-$(TEA_VERSION_TAG)-docs.tar.xz

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
	install -m 644 docs/generated/completion/tea.bash "$(DESTDIR)$(PREFIX)/share/bash-completion/completions/tea"
	install -d "$(DESTDIR)$(PREFIX)/share/zsh/site-functions"
	install -m 644 docs/generated/completion/_tea "$(DESTDIR)$(PREFIX)/share/zsh/site-functions/_tea"
	install -d "$(DESTDIR)$(PREFIX)/share/fish/vendor_completions.d"
	install -m 644 docs/generated/completion/tea.fish "$(DESTDIR)$(PREFIX)/share/fish/vendor_completions.d/tea.fish"
	install -d "$(DESTDIR)$(PREFIX)/share/powershell/Modules/tea"
	install -m 644 docs/generated/completion/tea.ps1 "$(DESTDIR)$(PREFIX)/share/powershell/Modules/tea/tea.ps1"

.PHONY: unit-test
unit-test:
	$(GO) test $(UNIT_PACKAGES)

.PHONY: integration-test
integration-test:
	@test -n "$$GITEA_TEA_TEST_URL" || { echo 'Set GITEA_TEA_TEST_URL to a disposable Gitea instance' >&2; exit 1; }
	@test -r "$$GITEA_TEA_TEST_TOKEN_FILE" || { echo 'Set GITEA_TEA_TEST_TOKEN_FILE to a readable administrator token file' >&2; exit 1; }
	@test -r "$$GITEA_TEA_TEST_REVIEWER_TOKEN_FILE" || { echo 'Set GITEA_TEA_TEST_REVIEWER_TOKEN_FILE to a readable reviewer token file' >&2; exit 1; }
	$(GO) test -count=1 -race -run '^TestGitea' $(PACKAGES)

.PHONY: test
test: unit-test

.PHONY: unit-test-coverage
unit-test-coverage:
	$(GO) test -race -cover -coverprofile coverage.out $(UNIT_PACKAGES) && echo "\n==>\033[32m Ok\033[m\n" || exit 1

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
	$(GO) install -v $(BUILDMODE) $(GOFLAGS)

.PHONY: build
build: $(EXECUTABLE)

$(EXECUTABLE): $(SOURCES)
	$(GO) build $(BUILDMODE) $(GOFLAGS) -o $@

.PHONY: build-image
build-image:
	docker build --build-arg VERSION=$(TEA_VERSION) -t gitea/tea:$(TEA_VERSION_TAG) .
