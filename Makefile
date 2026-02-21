GO_DIR := toolhub
BIN_DIR := bin
BIN := $(BIN_DIR)/toolhub

.PHONY: build

build:
	@mkdir -p $(BIN_DIR)
	go -C $(GO_DIR) build -o ../$(BIN) ./cmd/toolhub
