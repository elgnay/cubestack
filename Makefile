# Root Makefile — orchestration layer, delegates only, no build logic here
# All actual build commands are defined in each sub-project's Makefile

.PHONY: help test lint lint-config test-e2e build

.DEFAULT_GOAL := help

# ============================================================
# Global targets
# ============================================================

test: ## Run tests for all sub-projects
	$(MAKE) -C operator test

lint: ## Run linters for all sub-projects
	$(MAKE) -C operator lint

lint-config: ## Verify linter configuration for all sub-projects
	$(MAKE) -C operator lint-config

test-e2e: ## Run end-to-end tests for all sub-projects
	$(MAKE) -C operator test-e2e

build: ## Build all sub-projects
	$(MAKE) -C operator build

# ============================================================
# Help
# ============================================================

help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
