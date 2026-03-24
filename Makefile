VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
DIST     ?= dist
CMDS     := knoter knoter-auth
LDFLAGS  := -s -w -X main.version=$(VERSION)

PLATFORMS := \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64 \
	windows/arm64

.PHONY: all build dist dist-archives clean help $(PLATFORMS)

all: build

build:
	@for cmd in $(CMDS); do \
		echo "  building $$cmd"; \
		go build -ldflags="$(LDFLAGS)" -o $$cmd ./cmd/$$cmd; \
	done

# Cross-compile raw binaries into dist/
dist: $(PLATFORMS)

$(PLATFORMS):
	$(eval OS   := $(word 1,$(subst /, ,$@)))
	$(eval ARCH := $(word 2,$(subst /, ,$@)))
	$(eval EXT  := $(if $(filter windows,$(OS)),.exe,))
	@mkdir -p $(DIST)
	@for cmd in $(CMDS); do \
		echo "  $(OS)/$(ARCH)  $$cmd"; \
		GOOS=$(OS) GOARCH=$(ARCH) CGO_ENABLED=0 \
			go build -trimpath -ldflags="$(LDFLAGS)" \
			-o $(DIST)/$$cmd-$(OS)-$(ARCH)$(EXT) ./cmd/$$cmd; \
	done

# Create .tar.gz / .zip archives suitable for Homebrew (both binaries per archive)
dist-archives: dist
	@cd $(DIST) && for pair in \
		darwin/amd64 darwin/arm64 linux/amd64 linux/arm64; do \
		os=$$(echo $$pair | cut -d/ -f1); \
		arch=$$(echo $$pair | cut -d/ -f2); \
		name=knoter-$$os-$$arch; \
		echo "  archiving $$name.tar.gz"; \
		cp knoter-$$os-$$arch   knoter; \
		cp knoter-auth-$$os-$$arch knoter-auth; \
		tar czf $$name.tar.gz knoter knoter-auth; \
		rm knoter knoter-auth; \
	done
	@cd $(DIST) && for arch in amd64 arm64; do \
		name=knoter-windows-$$arch; \
		echo "  archiving $$name.zip"; \
		cp knoter-windows-$$arch.exe   knoter.exe; \
		cp knoter-auth-windows-$$arch.exe knoter-auth.exe; \
		zip $$name.zip knoter.exe knoter-auth.exe; \
		rm knoter.exe knoter-auth.exe; \
	done
	@cd $(DIST) && sha256sum *.tar.gz *.zip > checksums.txt
	@echo "Archives written to $(DIST)/"

clean:
	rm -f $(CMDS)
	rm -rf $(DIST)

help:
	@echo "Targets:"
	@echo "  build          Build knoter and knoter-auth for the current platform"
	@echo "  dist           Cross-compile raw binaries for all platforms"
	@echo "  dist-archives  Cross-compile + package into .tar.gz/.zip archives"
	@echo "  clean          Remove build artefacts"
	@echo ""
	@echo "Variables:"
	@echo "  VERSION=<tag>  Override version string (default: git describe)"
	@echo "  DIST=<dir>     Output directory (default: dist)"
	@echo ""
	@echo "Platforms:"
	@$(foreach p,$(PLATFORMS),echo "  $(p)";)
