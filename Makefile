APP := kiroku
CMD := ./cmd/kiroku
BUILD_DIR := .build
BUILD_BIN := $(BUILD_DIR)/$(APP)
GO ?= go
GOBIN := $(shell $(GO) env GOBIN)
GOPATH := $(shell $(GO) env GOPATH)

ifeq ($(strip $(GOBIN)),)
INSTALL_DIR := $(GOPATH)/bin
else
INSTALL_DIR := $(GOBIN)
endif

.PHONY: help test build install uninstall clean

help:
	@printf '%s\n' \
		'make test      - run all tests' \
		'make build     - build $(BUILD_BIN)' \
		'make install   - install $(APP) to $(INSTALL_DIR)' \
		'make uninstall - remove $(INSTALL_DIR)/$(APP)' \
		'make clean     - remove local build artifacts'

test:
	$(GO) test ./...

build:
	mkdir -p $(BUILD_DIR)
	$(GO) build -o $(BUILD_BIN) $(CMD)

install:
	mkdir -p $(INSTALL_DIR)
	GOBIN=$(INSTALL_DIR) $(GO) install $(CMD)

uninstall:
	rm -f $(INSTALL_DIR)/$(APP)

clean:
	rm -rf $(BUILD_DIR)
	rm -f $(APP)
