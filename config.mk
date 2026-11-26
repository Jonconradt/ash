# Shared configuration and variable defaults.

SITE_LOCALES ?=
UPDATE_DEPENDENCIES_ASSUME_YES ?= 0
MAKE_JOBS ?= $(shell getconf _NPROCESSORS_ONLN 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 1)

# Parallelize the lint/test dependency graph automatically. Preserve an explicit
# -j value from the user or CI. Sub-makes (MAKELEVEL > 0) inherit the parent's
# jobserver instead of forcing a new -j, which would reset jobserver mode and
# oversubscribe the CPU (GNU make exposes inherited jobserver flags in
# MAKEFLAGS only after makefiles are parsed, so they cannot be filtered here).
ifeq ($(MAKELEVEL),0)
ifeq ($(strip $(filter j -j%,$(MAKEFLAGS))),)
MAKEFLAGS += -j$(MAKE_JOBS)
endif
endif

# Test and fuzz defaults.
COVERAGE_MIN ?= 60
FUZZ_TIME ?= 10s

# Go and JavaScript lint/tool pins.
GOLANGCI_LINT_VERSION ?= v2.15.0
RUFF_VERSION ?= 0.17.6
MARKDOWNLINT_CLI2_VERSION ?= 0.24.2
HTML_VALIDATE_VERSION ?= 11.22.1
NODE_VERSION ?= lts/*
NVM_VERSION ?= v0.40.3

# Security and configuration tool pins.
GOSEC_VERSION ?= v2.30.0
GOVULNCHECK_VERSION ?= v1.10.0
STATICCHECK_VERSION ?= 2026.2.2
YAMLFMT_VERSION ?= v0.21.0
GITLEAKS_VERSION ?= v8.30.1
ACTIONLINT_VERSION ?= v1.7.12
PIP_AUDIT_VERSION ?= 2.10.1
CARGO_AUDIT_VERSION ?= 0.23.0

# Application and local build paths.
APP_NAME ?= ash
LOCAL_BIN_DIR ?= $(HOME)/.local/bin

# Make recipes run in their own shell, so expose an installed NVM Node to every
# Linux Make target (lint, test, release, and config itself) in the process PATH.
ifeq ($(shell uname -s),Linux)
NVM_NODE_DIR := $(shell ./scripts/dev/nvm-node-dir.sh "$(NODE_VERSION)")
ifneq ($(strip $(NVM_NODE_DIR)),)
export PATH := $(NVM_NODE_DIR):$(PATH)
endif
endif

LOCAL_BINARY_PATH ?= $(LOCAL_BIN_DIR)/$(APP_NAME)
BUILD_BIN_DIR ?= $(LOCAL_BIN_DIR)
PLUGINS_SRC_DIR ?= internal/app/ash_bootstrap/plugins_src
PLUGINS_BIN_DIR ?= bin/plugins
LOCAL_PLUGINS_DIR ?= $(if $(XDG_DATA_HOME),$(XDG_DATA_HOME),$(HOME)/.local/share)/ash/plugins
# Cross-compilation target for non-Go plugin toolchains (Rust); empty means "build
# natively for the host", used by local dev builds. release-build-one sets these to
# RELEASE_GOOS/RELEASE_ARCH so release plugin binaries match the release target.
PLUGIN_TARGET_GOOS ?=
PLUGIN_TARGET_GOARCH ?=
RELEASE_ARCH ?= $(shell uname -m | sed -e 's/^x86_64$$/amd64/' -e 's/^aarch64$$/arm64/')
RELEASE_OUTPUT_DIR ?= dist/release
RELEASE_TRANSLATION_CACHE ?= .cache/ash/site-translation-cache.json
RELEASE_PACKAGE_DIR ?= $(RELEASE_OUTPUT_DIR)
RELEASE_TARGET_ARCHES ?= amd64 arm64
RELEASE_TARGET_OSES ?= auto
RELEASE_MACOS_PKG ?= 1
RELEASE_SKIP_LOCAL_ARTIFACTS ?= 0
RELEASE_GOOS ?= darwin
RELEASE_FORMAT ?= pkg
# The interactive release dashboard is for local, manual releases, not CI logs.
RELEASE_WATCH ?= $(if $(CI),0,1)
RELEASE_WATCH_STRICT ?= 1
RELEASE_VERIFY_TARGET ?= verify
CHANNEL ?= beta
# Local builds report the nearest reachable tag, including prereleases.
ifeq ($(origin BUILD_VERSION),undefined)
BUILD_VERSION := $(shell git describe --tags --abbrev=0 2>/dev/null || printf 'dev')
endif
# Resolved once at parse time; recursive expansion would re-run git after release-publish tags HEAD.
ifeq ($(origin BUILD_COMMIT),undefined)
BUILD_COMMIT := $(shell git rev-parse HEAD 2>/dev/null || printf 'unknown')
endif
RELEASE_COMMIT ?= $(BUILD_COMMIT)
ifeq ($(origin LATEST_RELEASE_TAG),undefined)
LATEST_RELEASE_TAG := $(shell git tag --list 'v[0-9]*.[0-9]*.[0-9]*' --sort=-v:refname | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$$' | head -n1)
endif
ifeq ($(origin AUTO_RELEASE_VERSION),undefined)
AUTO_RELEASE_VERSION := $(shell ./scripts/release/next_version.sh "$(CHANNEL)")
endif
RELEASE_VERSION ?= $(AUTO_RELEASE_VERSION)
export RELEASE_VERSION RELEASE_COMMIT CHANNEL
RELEASE_PKG_NAME ?= $(APP_NAME)-$(RELEASE_VERSION)-darwin-$(RELEASE_ARCH).pkg
RELEASE_PKG_PATH ?= $(RELEASE_PACKAGE_DIR)/$(RELEASE_PKG_NAME)
RELEASE_INSTALL_PATH ?= /usr/local/bin
RELEASE_INSTALL_PATH_LINUX ?= /usr/bin
MAN_PAGE_PATH ?= docs/ash.1
LOCALIZED_MAN_DIR ?= docs/man
MAN_INSTALL_PATH_LINUX ?= /usr/share/man/man1
MAN_INSTALL_PATH_MACOS ?= /usr/local/share/man/man1
PLUGIN_INSTALL_PATH_LINUX ?= /usr/lib/ash/plugins
PLUGIN_INSTALL_PATH_MACOS ?= /usr/local/lib/ash/plugins
LICENSE_INSTALL_PATH_LINUX ?= /usr/share/doc/ash/copyright
LICENSE_INSTALL_PATH_RPM ?= /usr/share/licenses/ash/LICENSE
LICENSE_INSTALL_PATH_MACOS ?= /usr/local/share/licenses/ash/LICENSE
TARBALL_MAN_PATH ?= usr/share/man/man1
RELEASE_NOTES_PATH ?= $(RELEASE_OUTPUT_DIR)/release-notes.md
RELEASE_NOTES_INPUT ?=
RELEASE_NOTES_ATTEMPTS ?= 5
RELEASE_ARTIFACT_BASE ?= $(APP_NAME)-$(RELEASE_VERSION)-$(RELEASE_GOOS)-$(RELEASE_ARCH)
RELEASE_BINARY_PATH ?= $(RELEASE_OUTPUT_DIR)/$(RELEASE_ARTIFACT_BASE)
RELEASE_PLUGINS_BIN_DIR ?= $(RELEASE_OUTPUT_DIR)/$(RELEASE_ARTIFACT_BASE)-plugins
