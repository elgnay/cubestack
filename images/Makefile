# Images workspace Makefile — build & smoke the DevEnvironment base images.
# Build context is this directory (see AGENTS.md). Tags default to :smoke and are
# overridable per image (e.g. for CI publishing).

CONTAINER_TOOL ?= docker

IMG_SSH     ?= cubestack/ssh-ubuntu-server:smoke
IMG_JUPYTER ?= cubestack/jupyter:smoke

# Optional mirror overrides for CN/offline builds (empty = upstream).
APT_MIRROR    ?=
PIP_INDEX_URL ?=

APT_MIRROR_ARG    = $(if $(APT_MIRROR),--build-arg APT_MIRROR=$(APT_MIRROR))
PIP_INDEX_URL_ARG = $(if $(PIP_INDEX_URL),--build-arg PIP_INDEX_URL=$(PIP_INDEX_URL))

.PHONY: help build build-ssh build-jupyter smoke smoke-ssh smoke-jupyter

.DEFAULT_GOAL := help

build: build-ssh build-jupyter ## Build both images

build-ssh: ## Build the ssh-ubuntu-server image
	$(CONTAINER_TOOL) build $(APT_MIRROR_ARG) \
		-f ssh-ubuntu-server/Dockerfile -t $(IMG_SSH) .

build-jupyter: ## Build the jupyter image
	$(CONTAINER_TOOL) build $(APT_MIRROR_ARG) $(PIP_INDEX_URL_ARG) \
		-f jupyter/Dockerfile -t $(IMG_JUPYTER) .

smoke: build ## Build and run the full local Docker smoke
	IMG_SSH=$(IMG_SSH) IMG_JUPYTER=$(IMG_JUPYTER) hack/smoke.sh

smoke-ssh: build-ssh ## Build and smoke only the ssh-ubuntu-server image
	IMG_SSH=$(IMG_SSH) IMG_JUPYTER=$(IMG_JUPYTER) hack/smoke.sh --ssh

smoke-jupyter: build-jupyter ## Build and smoke only the jupyter image
	IMG_SSH=$(IMG_SSH) IMG_JUPYTER=$(IMG_JUPYTER) hack/smoke.sh --jupyter

help: ## Show this help message
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
