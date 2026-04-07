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

TEST_ORG    ?= plakar-gh-tests
SOURCE_REPO ?= plakar-e2e-source
STORE_DIR   ?= /tmp/plakar-e2e-store
PLAKAR_CFG  ?= /tmp/plakar-e2e-config

e2e-setup: ## Create source repo with realistic content on TEST_ORG
	@echo "==> Creating source repo $(TEST_ORG)/$(SOURCE_REPO)"
	gh repo create $(TEST_ORG)/$(SOURCE_REPO) --public --description "plakar e2e test source"
	@echo "==> Pushing files"
	@tmp=$$(mktemp -d) && \
	  git -C $$tmp init -b main && \
	  git -C $$tmp config user.email "e2e@plakar-test.local" && \
	  git -C $$tmp config user.name "plakar-e2e" && \
	  mkdir -p $$tmp/src $$tmp/.github/workflows && \
	  printf '# plakar-e2e-source\n\nTest repository for plakar backup/restore e2e tests.\n' > $$tmp/README.md && \
	  printf 'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("hello")\n}\n' > $$tmp/src/main.go && \
	  printf 'name: CI\non: [push]\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - uses: actions/checkout@v4\n' > $$tmp/.github/workflows/ci.yml && \
	  git -C $$tmp add . && \
	  git -C $$tmp commit -m "feat: initial commit" && \
	  git -C $$tmp tag v1.0.0 && \
	  git -C $$tmp remote add origin https://github.com/$(TEST_ORG)/$(SOURCE_REPO).git && \
	  git -C $$tmp push -u origin main --tags && \
	  rm -rf $$tmp
	@echo "==> Creating labels"
	gh label create bug --repo $(TEST_ORG)/$(SOURCE_REPO) --color d73a4a 2>/dev/null || true
	gh label create enhancement --repo $(TEST_ORG)/$(SOURCE_REPO) --color a2eeef 2>/dev/null || true
	gh label create documentation --repo $(TEST_ORG)/$(SOURCE_REPO) --color 0075ca 2>/dev/null || true
	@echo "==> Creating issues"
	gh issue create --repo $(TEST_ORG)/$(SOURCE_REPO) --title "Open bug" --body "This bug is still open." --label bug
	gh issue create --repo $(TEST_ORG)/$(SOURCE_REPO) --title "Fixed bug" --body "This bug was fixed." --label bug
	gh issue close 2 --repo $(TEST_ORG)/$(SOURCE_REPO)
	gh issue create --repo $(TEST_ORG)/$(SOURCE_REPO) --title "Feature request" --body "Please add this feature." --label enhancement
	@echo "==> Creating PR (not backed up — documents the gap)"
	@tmp=$$(mktemp -d) && \
	  gh repo clone $(TEST_ORG)/$(SOURCE_REPO) $$tmp -- --quiet && \
	  git -C $$tmp config user.email "e2e@plakar-test.local" && \
	  git -C $$tmp config user.name "plakar-e2e" && \
	  git -C $$tmp checkout -b feat/not-merged && \
	  printf 'not merged\n' >> $$tmp/README.md && \
	  git -C $$tmp add README.md && \
	  git -C $$tmp commit -m "feat: not merged change" && \
	  git -C $$tmp push -u origin feat/not-merged && \
	  gh pr create --repo $(TEST_ORG)/$(SOURCE_REPO) \
	    --title "PR not backed up by plakar" \
	    --body "Pull requests are not backed up by the integration." \
	    --head feat/not-merged --base main && \
	  rm -rf $$tmp
	@echo "==> Source repo ready: https://github.com/$(TEST_ORG)/$(SOURCE_REPO)"

e2e-backup: ## Backup SOURCE_REPO to STORE_DIR
	@test -n "$(GITHUB_TOKEN)" || (echo "ERROR: GITHUB_TOKEN is not set"; exit 1)
	@echo "==> Installing plugin"
	$(MAKE) install
	@echo "==> Creating plakar store at $(STORE_DIR)"
	rm -rf $(STORE_DIR)
	plakar -config $(PLAKAR_CFG) at fs://$(STORE_DIR) create -plaintext
	@echo "==> Adding source"
	plakar -config $(PLAKAR_CFG) source add e2e-src \
	  "github://$(TEST_ORG)/$(SOURCE_REPO)" token=$(GITHUB_TOKEN)
	@echo "==> Running backup"
	plakar -config $(PLAKAR_CFG) at fs://$(STORE_DIR) backup @e2e-src
	@echo "==> Backup complete"
	plakar -config $(PLAKAR_CFG) at fs://$(STORE_DIR) snapshots

e2e-restore: ## Restore latest snapshot to a new repo on TEST_ORG
	@test -n "$(GITHUB_TOKEN)" || (echo "ERROR: GITHUB_TOKEN is not set"; exit 1)
	$(eval RESTORE_REPO := plakar-e2e-restore-$(shell date +%s))
	@echo "==> Restoring to $(TEST_ORG)/$(RESTORE_REPO)"
	plakar -config $(PLAKAR_CFG) destination add e2e-dst \
	  "github://$(TEST_ORG)" token=$(GITHUB_TOKEN) repo=$(RESTORE_REPO)
	plakar -config $(PLAKAR_CFG) at fs://$(STORE_DIR) restore -latest -to @e2e-dst
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

e2e-teardown: ## Delete test repos and clean up local state
	@echo "==> Deleting source repo"
	gh repo delete $(TEST_ORG)/$(SOURCE_REPO) --yes 2>/dev/null || true
	@echo "==> Deleting restore repos"
	@gh repo list $(TEST_ORG) --json name --jq '.[].name' 2>/dev/null \
	  | grep '^plakar-e2e-restore-' \
	  | xargs -I{} gh repo delete $(TEST_ORG)/{} --yes 2>/dev/null || true
	@echo "==> Cleaning up local state"
	rm -rf $(STORE_DIR) $(PLAKAR_CFG) /tmp/plakar-e2e-restore-repo
	@echo "==> Teardown complete"

e2e: e2e-setup e2e-backup e2e-restore e2e-verify e2e-teardown ## Full e2e cycle
