# Images workspace Makefile — build, smoke & publish the DevEnvironment base images.
# Build context is this directory (see AGENTS.md). Each image has one name: a build is
# tagged at the reference it publishes under, and the push targets add :latest to — never
# rebuild — the image the smoke just accepted.

CONTAINER_TOOL ?= docker

# Optional mirror overrides for CN/offline builds (empty = upstream).
APT_MIRROR    ?=
PIP_INDEX_URL ?=

# Base release of the self-authored ssh image. It selects the build base AND names the
# published repository (decision doc §5), so the name cannot outlive the base.
UBUNTU_VERSION ?= 22.04

APT_MIRROR_ARG    = $(if $(APT_MIRROR),--build-arg APT_MIRROR=$(APT_MIRROR))
PIP_INDEX_URL_ARG = $(if $(PIP_INDEX_URL),--build-arg PIP_INDEX_URL=$(PIP_INDEX_URL))

# Publishing coordinates (decision doc §5). One name per image: a build is tagged at the
# reference it will be published under, so the image `make smoke` accepts is the image
# `make push` ships — push adds :latest to that image rather than rebuilding it.
# TAG defaults to the short commit SHA, so a bare `make build` yields a traceable,
# non-floating reference. It names the last commit, not the working tree, and is resolved
# per make invocation — keep build/smoke/push in one go (`make push` does all three) or
# pass an explicit TAG (§5 schemes). Publishing an older commit moves :latest backwards.
# Authenticate first: docker login $(REGISTRY).
REGISTRY ?= harbor.isuanova.com
PROJECT  ?= suanova
TAG      ?= $(shell git rev-parse --short HEAD)

SSH_REPO     = $(REGISTRY)/$(PROJECT)/ssh-ubuntu$(UBUNTU_VERSION)
# -minimal is the docker-stacks stack variant this overlay is built on (decision doc §5); unlike
# UBUNTU_VERSION it is not a build arg, so the name is a literal here and the base stays pinned in
# jupyter/Dockerfile.
JUPYTER_REPO = $(REGISTRY)/$(PROJECT)/jupyter-minimal

IMG_SSH     ?= $(SSH_REPO):$(TAG)
IMG_JUPYTER ?= $(JUPYTER_REPO):$(TAG)

# The moving publish tag: added by `make push`, never produced by `make build`, so a
# local :latest means it came from a publish.
LATEST_SSH     = $(SSH_REPO):latest
LATEST_JUPYTER = $(JUPYTER_REPO):latest

.PHONY: help build build-ssh build-jupyter smoke smoke-ssh smoke-jupyter \
	push push-ssh push-jupyter

.DEFAULT_GOAL := help

build: build-ssh build-jupyter ## Build both images

build-ssh: ## Build the self-authored ssh image
	$(CONTAINER_TOOL) build $(APT_MIRROR_ARG) --build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		-f ssh-ubuntu-server/Dockerfile -t $(IMG_SSH) .

build-jupyter: ## Build the jupyter image
	$(CONTAINER_TOOL) build $(APT_MIRROR_ARG) $(PIP_INDEX_URL_ARG) \
		-f jupyter/Dockerfile -t $(IMG_JUPYTER) .

smoke: build ## Build and run the full local Docker smoke
	IMG_SSH=$(IMG_SSH) IMG_JUPYTER=$(IMG_JUPYTER) hack/smoke.sh

smoke-ssh: build-ssh ## Build and smoke only the self-authored ssh image
	IMG_SSH=$(IMG_SSH) IMG_JUPYTER=$(IMG_JUPYTER) hack/smoke.sh --ssh

smoke-jupyter: build-jupyter ## Build and smoke only the jupyter image
	IMG_SSH=$(IMG_SSH) IMG_JUPYTER=$(IMG_JUPYTER) hack/smoke.sh --jupyter

push: push-ssh push-jupyter ## Build then publish both images ($(TAG) + :latest)

push-ssh: build-ssh ## Build, then publish the self-authored ssh image (adds :latest)
	$(CONTAINER_TOOL) tag $(IMG_SSH) $(LATEST_SSH)
	$(CONTAINER_TOOL) push $(IMG_SSH)
	$(CONTAINER_TOOL) push $(LATEST_SSH)

push-jupyter: build-jupyter ## Build, then publish the jupyter image (adds :latest)
	$(CONTAINER_TOOL) tag $(IMG_JUPYTER) $(LATEST_JUPYTER)
	$(CONTAINER_TOOL) push $(IMG_JUPYTER)
	$(CONTAINER_TOOL) push $(LATEST_JUPYTER)

help: ## Show this help message
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
