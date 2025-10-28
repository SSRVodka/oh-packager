GO=go

PROJECT_NAME=oh-packager

OUTPUT_DIR := build
BIN_DIR := ${OUTPUT_DIR}/bin
CONF_DIR := ${OUTPUT_DIR}/config
SCRIPT_DIR := ${OUTPUT_DIR}/scripts
RAW_SCRIPT_DIR := scripts

CMD_DIR := cmd

# Debug Build: eliminate inlining
GCFLAGS=-N -l


all: oh-pkgtool oh-pkgserver oh-pkgmgr

oh-pkgtool:
	@mkdir -p $(BIN_DIR)
	${GO} build -gcflags "${GCFLAGS}" -o $(BIN_DIR)/$@ $(CMD_DIR)/pkgtool/main.go

oh-pkgserver:
	@mkdir -p $(BIN_DIR)
	${GO} build -gcflags "${GCFLAGS}" -o $(BIN_DIR)/$@ $(CMD_DIR)/pkgserver/main.go

oh-pkgmgr:
	@mkdir -p $(BIN_DIR)
	${GO} build -gcflags "${GCFLAGS}" -o $(BIN_DIR)/$@ $(CMD_DIR)/pkgmgr/main.go


copyconf:
	@echo "Copy configurations to dir"
	@mkdir -p $(CONF_DIR)
	@cp -f config/*.yaml config/*.conf $(CONF_DIR)
	@cp -f config/.env $(CONF_DIR) 2>/dev/null || true

copyscripts:
	@echo "Copy scripts to dir"
	@cp -rf scripts $(OUTPUT_DIR)

test:
	${GO} clean -testcache
	# ${GO} test ${PROJECT_NAME}/test/package/controlPlane
	# ${GO} test ${PROJECT_NAME}/test/package/controlPlane/managers
	# ${GO} test -v ${PROJECT_NAME}/test/package/kubelet
	# ${GO} test -v ${PROJECT_NAME}/test/package/messagequeue/subscriber

clean:
	@rm -rf $(OUTPUT_DIR)

help:
	@echo "Usage: make [target]"
	@echo "Targets:"
	@echo "  build    - compile project"
	@echo "  clean    - clean output files"
	@echo "  help     - print this message"

.PHONY: all build test clean help

