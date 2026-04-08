GO ?= go
EXT ?=
OS   := $(shell go env GOOS)
ARCH := $(shell go env GOARCH)
NAME := integration-github
VERSION := $(shell grep '^version:' manifest.yaml | awk '{print $$2}')
PTAR := $(NAME)_$(VERSION)_$(OS)_$(ARCH).ptar

.PHONY: all build test pkg install clean

all: build

build:
	$(GO) build -v -o $(NAME)-importer$(EXT) ./importer
	$(GO) build -v -o $(NAME)-exporter$(EXT) ./exporter

test:
	$(GO) test -race -v ./...

pkg: build
	rm -f $(PTAR)
	plakar pkg create manifest.yaml

install: pkg
	plakar pkg rm $(NAME) 2>/dev/null || true
	plakar pkg add ./$(PTAR)

clean:
	rm -f $(NAME)-importer$(EXT) $(NAME)-exporter$(EXT) *.ptar coverage.out

## E2E tests — requires GITHUB_TOKEN and plakar v1.0.6+ installed
## Source repo: https://github.com/plakar-gh-tests/plakar-e2e-source (pre-created fixture)

TEST_ORG    ?= plakar-gh-tests
SOURCE_REPO ?= plakar-e2e-source
STORE_DIR   ?= /tmp/plakar-e2e-store
PLAKAR_CFG  ?= /tmp/plakar-e2e-config

e2e-backup: ## Backup SOURCE_REPO to STORE_DIR
	@test -n "$(GITHUB_TOKEN)" || (echo "ERROR: GITHUB_TOKEN is not set"; exit 1)
	@echo "==> Installing plugin"
	$(MAKE) install
	@echo "==> Creating plakar store at $(STORE_DIR)"
	rm -rf $(STORE_DIR)
	plakar at fs://$(STORE_DIR) create -plaintext
	@echo "==> Registering source (uses default plakar config)"
	plakar source rm e2e-src 2>/dev/null || true
	plakar source add e2e-src \
	  "github://$(TEST_ORG)/$(SOURCE_REPO)" token=$(GITHUB_TOKEN)
	@echo "==> Running backup"
	plakar at fs://$(STORE_DIR) backup @e2e-src
	@echo "==> Backup complete"
	plakar at fs://$(STORE_DIR) ls

e2e-restore: ## Restore latest snapshot to a new repo on TEST_ORG
	@test -n "$(GITHUB_TOKEN)" || (echo "ERROR: GITHUB_TOKEN is not set"; exit 1)
	$(eval RESTORE_REPO := plakar-e2e-restore-$(shell date +%s))
	@echo "==> Registering destination (uses default plakar config)"
	plakar destination rm e2e-dst 2>/dev/null || true
	plakar destination add e2e-dst \
	  "github://$(TEST_ORG)" token=$(GITHUB_TOKEN) repo=$(RESTORE_REPO)
	@echo "==> Restoring to $(TEST_ORG)/$(RESTORE_REPO)"
	$(eval SNAP := $(shell plakar at fs://$(STORE_DIR) ls | awk 'NR==1{print $$2}'))
	plakar at fs://$(STORE_DIR) restore -to @e2e-dst "$(SNAP):/"
	@echo "==> Restored to: https://github.com/$(TEST_ORG)/$(RESTORE_REPO)"
	@echo $(RESTORE_REPO) > /tmp/plakar-e2e-restore-repo

e2e-verify: ## Verify the restored repo has expected content
	@test -f /tmp/plakar-e2e-restore-repo || (echo "ERROR: run make e2e-restore first"; exit 1)
	$(eval RESTORE_REPO := $(shell cat /tmp/plakar-e2e-restore-repo))
	@echo "==> Verifying $(TEST_ORG)/$(RESTORE_REPO)"
	@pass=0; fail=0; \
	check() { \
	  if eval "$$2"; then \
	    echo "  [PASS] $$1"; pass=$$((pass+1)); \
	  else \
	    echo "  [FAIL] $$1"; fail=$$((fail+1)); \
	  fi; \
	}; \
	check "README.md exists" \
	  "gh api repos/$(TEST_ORG)/$(RESTORE_REPO)/contents/README.md --silent 2>/dev/null"; \
	check "src/main.go exists" \
	  "gh api repos/$(TEST_ORG)/$(RESTORE_REPO)/contents/src/main.go --silent 2>/dev/null"; \
	check ".github/workflows/ci.yml exists" \
	  "gh api repos/$(TEST_ORG)/$(RESTORE_REPO)/contents/.github/workflows/ci.yml --silent 2>/dev/null"; \
	open=$$(gh issue list --repo $(TEST_ORG)/$(RESTORE_REPO) --state open --json number --jq length 2>/dev/null); \
	closed=$$(gh issue list --repo $(TEST_ORG)/$(RESTORE_REPO) --state closed --json number --jq length 2>/dev/null); \
	check "2 open issues" "[ \"$$open\" = \"2\" ]"; \
	check "1 closed issue" "[ \"$$closed\" = \"1\" ]"; \
	echo ""; \
	if [ $$fail -eq 0 ]; then echo "PASS ($$pass checks)"; else echo "FAIL ($$fail failed, $$pass passed)"; exit 1; fi

e2e-teardown: ## Delete restore repos and clean up local state
	@echo "==> Removing plakar source/destination config"
	plakar source rm e2e-src 2>/dev/null || true
	plakar destination rm e2e-dst 2>/dev/null || true
	@echo "==> Deleting restore repos"
	@gh repo list $(TEST_ORG) --json name --jq '.[].name' 2>/dev/null \
	  | grep '^plakar-e2e-restore-' \
	  | xargs -I{} gh repo delete $(TEST_ORG)/{} --yes 2>/dev/null || true
	@echo "==> Cleaning up local state"
	rm -rf $(STORE_DIR) /tmp/plakar-e2e-restore-repo
	@echo "==> Teardown complete"

e2e: e2e-backup e2e-restore e2e-verify e2e-teardown ## Full e2e cycle
