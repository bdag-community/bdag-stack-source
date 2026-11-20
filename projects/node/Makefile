
EXECUTABLE := bdag
EXECUTABLE_QX := qx
RELEASE_TAG ?= $(shell git describe --tags --exact-match --match 'node-*' 2>/dev/null)
VERSION := $(RELEASE_TAG)
LDFLAG_TAG = -X github.com/BlockdagNetworkLabs/bdag/version.ReleaseTag=$(RELEASE_TAG)
GOFLAGS_DEV = -trimpath -ldflags "$(LDFLAG_TAG)"
GOFLAGS_RELEASE = -trimpath -ldflags "$(LDFLAG_TAG)"
GOFLAGS_RELEASE_QX = -trimpath -ldflags "$(LDFLAG_TAG)"
GOBIN = ./build/bin

# Enable CGO by default
CGO_ENABLED ?= 1
export CGO_ENABLED

# Cross-compiler definitions for macOS host
ifeq ($(shell uname -s),Darwin)
	LINUX_AMD64_CC = x86_64-unknown-linux-gnu-gcc
	WIN_AMD64_CC = x86_64-w64-mingw32-gcc
else
	LINUX_AMD64_CC = gcc
	WIN_AMD64_CC = x86_64-w64-mingw32-gcc
endif

# Automatic detection operating system
ifeq ($(OS),Windows_NT)
	OUTPUT_SUFFIX=.exe
else
	OUTPUT_SUFFIX=
endif

UNIX_EXECUTABLES := \
	build/release/darwin/amd64/bin/$(EXECUTABLE) \
	build/release/darwin/arm64/bin/$(EXECUTABLE) \
	build/release/linux/amd64/bin/$(EXECUTABLE)
WIN_EXECUTABLES := \
	build/release/windows/amd64/bin/$(EXECUTABLE).exe

EXECUTABLES=$(UNIX_EXECUTABLES) $(WIN_EXECUTABLES)

DEV_EXECUTABLES := \
	build/dev/darwin/amd64/bin/$(EXECUTABLE) \
	build/dev/darwin/arm64/bin/$(EXECUTABLE) \
	build/dev/linux/amd64/bin/$(EXECUTABLE) \
	build/dev/windows/amd64/bin/$(EXECUTABLE).exe

COMPRESSED_EXECUTABLES := \
	$(UNIX_EXECUTABLES:%=%.bdag.tar.gz) \
	$(WIN_EXECUTABLES:%.exe=%.bdag.zip) \
	$(WIN_EXECUTABLES:%.exe=%.bdag.cn.zip)


RELEASE_TARGETS=$(EXECUTABLES) $(COMPRESSED_EXECUTABLES)

DEV_TARGETS=$(DEV_EXECUTABLES)

ZMQ = FALSE

DEBUG = OFF

.PHONY: bdag qx release deps setup-cross clean checkversion all nodeworker bdag-cpuminer bdag-build bdag-tag-check

checkversion:
	@$(MAKE) bdag

bdag-tag-check:
	@tag="$(strip $(RELEASE_TAG))"; \
	case "$$tag" in \
		node-*|dev) ;; \
		*) \
			echo "Error: builds require an exact node-* release tag or the explicit dev marker (got '$$tag')." >&2; \
			exit 1; \
			;; \
	esac

setup-cross:
ifeq ($(shell uname -s),Darwin)
	@echo "Detected macOS. Installing cross-compilers via Homebrew..."
	@brew tap messense/macos-cross-toolchains
	@brew install x86_64-unknown-linux-gnu mingw-w64
	@echo "Done. You can now run 'make release'."
else
	@echo "Manual installation required for non-macOS systems."
	@echo "Linux: apt-get install gcc-multilib g++-multilib"
endif

deps:
	@echo "Downloading Go dependencies..."
	@go mod download
	@echo "Done."

bdag: bdag-build
	@echo "Done building."
	@echo "  $(shell $(GOBIN)/bdag --version))"
	@echo "Run \"$(GOBIN)/bdag\" to launch."

bdag-build: bdag-tag-check
ifeq ($(ZMQ),TRUE)
	@echo "Enable ZMQ"
	@go build -o $(GOBIN)/bdag$(OUTPUT_SUFFIX) $(GOFLAGS_DEV) -tags=zmq "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"
else ifeq ($(DEBUG),ON)
	@echo "Enable DEBUG"
	@go build -o $(GOBIN)/bdag$(OUTPUT_SUFFIX) $(GOFLAGS_DEV) -gcflags="all=-N -l" "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"
else
	@go build -o $(GOBIN)/bdag$(OUTPUT_SUFFIX) $(GOFLAGS_DEV) "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"
endif

qx: bdag-tag-check
	@go build -o $(GOBIN)/qx $(GOFLAGS_DEV) "github.com/BlockdagNetworkLabs/bdag/cmd/qx"
nodeworker: bdag-tag-check
	@go build -o $(GOBIN)/nodeworker $(GOFLAGS_DEV) "github.com/BlockdagNetworkLabs/bdag/cmd/nodeworker"
bdag-cpuminer: bdag-tag-check
	@go build -o $(GOBIN)/bdag-cpuminer $(GOFLAGS_DEV) "github.com/BlockdagNetworkLabs/bdag/cmd/bdag-cpuminer"
all: bdag-build nodeworker bdag-cpuminer
# Explicit Release Build Rules
build/release/darwin/amd64/bin/$(EXECUTABLE): bdag-tag-check
	@echo Build $@
	@GOOS=darwin GOARCH=amd64 go build $(GOFLAGS_RELEASE) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"

build/release/darwin/arm64/bin/$(EXECUTABLE): bdag-tag-check
	@echo Build $@
	@GOOS=darwin GOARCH=arm64 go build $(GOFLAGS_RELEASE) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"

build/release/linux/amd64/bin/$(EXECUTABLE): bdag-tag-check
	@echo Build $@
	@CC=$(LINUX_AMD64_CC) GOOS=linux GOARCH=amd64 go build $(GOFLAGS_RELEASE) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"

build/release/windows/amd64/bin/$(EXECUTABLE).exe: bdag-tag-check
	@echo Build $@
	@CC=$(WIN_AMD64_CC) GOOS=windows GOARCH=amd64 go build $(GOFLAGS_RELEASE) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"

build/release/darwin/amd64/bin/$(EXECUTABLE_QX): bdag-tag-check
	@echo Build $@
	@GOOS=darwin GOARCH=amd64 go build $(GOFLAGS_RELEASE_QX) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/qx"

build/release/darwin/arm64/bin/$(EXECUTABLE_QX): bdag-tag-check
	@echo Build $@
	@GOOS=darwin GOARCH=arm64 go build $(GOFLAGS_RELEASE_QX) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/qx"

build/release/linux/amd64/bin/$(EXECUTABLE_QX): bdag-tag-check
	@echo Build $@
	@CC=$(LINUX_AMD64_CC) GOOS=linux GOARCH=amd64 go build $(GOFLAGS_RELEASE_QX) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/qx"

build/release/windows/amd64/bin/$(EXECUTABLE_QX).exe: bdag-tag-check
	@echo Build $@
	@CC=$(WIN_AMD64_CC) GOOS=windows GOARCH=amd64 go build $(GOFLAGS_RELEASE_QX) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/qx"

# Dev Build Rules
build/dev/darwin/amd64/bin/$(EXECUTABLE): bdag-tag-check
	@GOOS=darwin GOARCH=amd64 go build $(GOFLAGS_DEV) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"

build/dev/darwin/arm64/bin/$(EXECUTABLE): bdag-tag-check
	@GOOS=darwin GOARCH=arm64 go build $(GOFLAGS_DEV) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"

build/dev/linux/amd64/bin/$(EXECUTABLE): bdag-tag-check
	@CC=$(LINUX_AMD64_CC) GOOS=linux GOARCH=amd64 go build $(GOFLAGS_DEV) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"

build/dev/windows/amd64/bin/$(EXECUTABLE).exe: bdag-tag-check
	@CC=$(WIN_AMD64_CC) GOOS=windows GOARCH=amd64 go build $(GOFLAGS_DEV) -o $@ "github.com/BlockdagNetworkLabs/bdag/cmd/bdag"

%.bdag.zip: %.exe
	@echo zip $(EXECUTABLE)-$(VERSION)-$(OS)-$(ARCH)
	@zip $(EXECUTABLE)-$(VERSION)-$(OS)-$(ARCH).zip "$<"

%.bdag.cn.zip: %.exe
	@echo zip $(EXECUTABLE)-$(VERSION)-$(OS)-$(ARCH)
	@zip -j $(EXECUTABLE)-$(VERSION)-$(OS)-$(ARCH).cn.zip "$<" script/win/start.bat

%.bdag.tar.gz : %
	@echo tar $(EXECUTABLE)-$(VERSION)-$(OS)-$(ARCH)
	@tar -zcvf $(EXECUTABLE)-$(VERSION)-$(OS)-$(ARCH).tar.gz "$<"

%.qx.tar.gz : %
	@echo qx.tar.gz: $(@)
	@tar -zcvf $(EXECUTABLE_QX)-$(VERSION)-$(OS)-$(ARCH).tar.gz "$<"
%.qx.zip: %.exe
	@echo qx.zip: $(@)
	@echo zip $(EXECUTABLE_QX)-$(VERSION)-$(OS)-$(ARCH)
	@zip $(EXECUTABLE_QX)-$(VERSION)-$(OS)-$(ARCH).zip "$<"


release: clean checkversion
	@echo "Build release version : $(VERSION)"
	@mkdir -p build/release/darwin/amd64/bin build/release/darwin/arm64/bin build/release/linux/amd64/bin build/release/windows/amd64/bin
	@RELEASE_TAG="$$(git describe --tags --exact-match --match 'node-*' 2>/dev/null)"; \
	if [ -z "$$RELEASE_TAG" ] || ! echo "$$RELEASE_TAG" | grep -q '^node-'; then \
		echo "Error: release requires an exact node-* tag on HEAD." >&2; \
		exit 1; \
	fi; \
	$(MAKE) RELEASE_TAG="$$RELEASE_TAG" $(RELEASE_TARGETS)
	@shasum -a 512 $(EXECUTABLES) > release-$(VERSION)_checksum.txt

# Development build — does not require an exact node-* release tag.
# Uses 'dev' version with git metadata for provenance.
# The output is marked unqualified so packaging cannot mistake it for a release.
dev: clean
	@echo "Building development binary (unqualified)..."
	$(MAKE) RELEASE_TAG=dev bdag-build nodeworker bdag-cpuminer
	@echo "✅ Development build complete — not a release artifact"

checksum: checkversion
	@cat release-$(VERSION)_checksum.txt|shasum -c

clean:
	@rm -f *.zip
	@rm -f *.tar.gz
	@rm -rf ./build/bin/*
	@rm -rf ./build/release
	@rm -rf ./build/dev
