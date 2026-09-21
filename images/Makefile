# Images workspace Makefile — build, smoke & publish the DevEnvironment base images.
# Build context is this directory (see AGENTS.md). Each image has one name: build and
# smoke work on one platform in the local store, and push publishes that name plus
# :latest as a single multi-arch index (PLATFORMS). buildx cannot load locally and
# push in one step, so the published layers are a fresh build of the same source
# rather than the very image the local smoke accepted.

CONTAINER_TOOL ?= docker

# The architecture every published image must run on: the cluster's nodes are amd64, and the
# machine building them may not be. Both Dockerfiles start from a multi-arch base (ubuntu,
# quay.io/jupyter), and without --platform `docker build` resolves that base to the *host*
# architecture — so a build on an arm64 host publishes an arm64-only image and every amd64 node
# then fails to pull it ("no match for platform in manifest"). Naming the platform makes the
# build independent of the host; PLATFORM=linux/arm64 is the explicit opt-in to build for
# another one. `docker buildx imagetools inspect --raw <ref>` shows what a registry received.
PLATFORM ?= linux/amd64

# What `make push` publishes, as one multi-arch index per image: a node then pulls the
# manifest for its own architecture rather than the builder's, which is the difference
# between an amd64-only publish and one an arm64 laptop can run. `make push
# PLATFORMS=linux/amd64` narrows it back to a single platform.
#
# Needs a builder that can build more than one platform — Docker Desktop's can; on a
# plain docker install run `docker buildx create --use` first (and install QEMU, e.g.
# the docker/setup-qemu-action in CI) or the build fails on the non-native platform.
PLATFORMS ?= linux/amd64 linux/arm64

# One --platform flag per entry. Derives nothing from PLATFORM: build/smoke and publish
# are separate flows and neither should quietly change the other's targets.
PLATFORMS_ARG = $(foreach p,$(PLATFORMS),--platform $(p))

# Buildkit's provenance and SBOM attestations are off. Harbor rejects an index carrying
# them with a 404 on the manifest PUT — "artifact ... not found" — even when every
# manifest it references resolves on its own (seen 2026-09-21 publishing
# suanova/jupyter-minimal, where the same index without them pushed fine). They are
# metadata a customer does not pull, so dropping them costs nothing.
BUILDX_ATTEST = --provenance=false --sbom=false

# Optional mirror overrides for CN/offline builds (empty = upstream).
APT_MIRROR    ?=
PIP_INDEX_URL ?=

# Base-image build args, defaulting to this registry's own mirrored copies of the upstream
# bases. A frozen mirror is what makes a build reproducible: `ubuntu:22.04` is refreshed
# upstream monthly, so resolving FROM it directly can yield a different base than the one
# the platform serves — and only works where upstream is reachable. Both mirrors are
# multi-arch, so naming them does not narrow what PLATFORMS can build. Pass `BASE_ARGS=`
# to resolve FROM upstream instead. The jupyter date tracks the pin in jupyter/Dockerfile;
# bump both together.
BASE_ARGS ?= \
	--build-arg JUPYTER_BASE=$(REGISTRY)/$(PROJECT)/jupyter-minimal-notebook:2026-09-07 \
	--build-arg UBUNTU_BASE=$(REGISTRY)/$(PROJECT)/ubuntu:$(UBUNTU_VERSION)

# Base release of the self-authored ssh image. It selects the build base AND names the
# published repository (decision doc §5), so the name cannot outlive the base.
UBUNTU_VERSION ?= 22.04

APT_MIRROR_ARG    = $(if $(APT_MIRROR),--build-arg APT_MIRROR=$(APT_MIRROR))
PIP_INDEX_URL_ARG = $(if $(PIP_INDEX_URL),--build-arg PIP_INDEX_URL=$(PIP_INDEX_URL))

# Publishing coordinates (decision doc §5). One name per image: a build is tagged at the
# reference it will be published under, and push publishes that same name plus :latest as
# one multi-arch index. TAG defaults to the short commit SHA, so a bare `make build` yields a
# traceable, non-floating reference. It names the last commit, not the working tree, and is
# resolved per make invocation — pass an explicit TAG if a commit lands between build and
# push (§5 schemes). Publishing an older commit moves :latest backwards.
# Authenticate first: docker login $(REGISTRY).
#
# TAG has no fallback: a build must name a real commit, so there is nothing to
# substitute when git metadata is missing (a source tarball). git rev-parse then
# yields nothing and every reference would end at the ':'; check-tag fails with
# that reason instead, rather than inventing a non-reproducible tag.
REGISTRY ?= harbor.isuanova.com
PROJECT  ?= suanova
TAG      ?= $(shell git rev-parse --short HEAD 2>/dev/null)

SSH_REPO     = $(REGISTRY)/$(PROJECT)/ssh-ubuntu$(UBUNTU_VERSION)
# -minimal is the docker-stacks stack variant this overlay is built on (decision doc §5); unlike
# UBUNTU_VERSION it does not name the published repository, so the variant is a literal here and
# the base stays pinned in jupyter/Dockerfile.
JUPYTER_REPO = $(REGISTRY)/$(PROJECT)/jupyter-minimal

IMG_SSH     ?= $(SSH_REPO):$(TAG)
IMG_JUPYTER ?= $(JUPYTER_REPO):$(TAG)

# The moving publish tag: added by `make push`, never produced by `make build`, so a
# local :latest means it came from a publish.
LATEST_SSH     = $(SSH_REPO):latest
LATEST_JUPYTER = $(JUPYTER_REPO):latest

.PHONY: help check-tag check-platform check-platforms build build-ssh build-jupyter smoke smoke-ssh \
	smoke-jupyter push push-ssh push-jupyter

.DEFAULT_GOAL := help

# Every build/smoke/push target funnels through build-ssh or build-jupyter, so this
# is the one place a missing TAG has to be caught.
check-tag:
	@test -n "$(TAG)" || { \
		echo "make -C images: TAG is empty - 'git rev-parse --short HEAD' returned nothing" >&2; \
		echo "                (not a git checkout?); pass TAG=<tag> explicitly." >&2; \
		exit 1; }

# `docker build` loads what it builds into the local store, so it takes exactly one
# platform, and a list would surface as an opaque BuildKit error about the driver. That
# is what build/smoke want: one image they can run. Publishing several is a different
# flow with its own guard below.
check-platform:
	@test -n "$(PLATFORM)" || { \
		echo "make -C images: PLATFORM is empty - name the platform the images run on (e.g. linux/amd64)." >&2; \
		exit 1; }
	@case "$(PLATFORM)" in \
		*,*) echo "make -C images: PLATFORM takes one platform, not a list (got '$(PLATFORM)')." >&2; \
		     echo "                To publish several at once, use 'make push PLATFORMS=<list>'." >&2; \
		     exit 1;; \
	esac

check-platforms:
	@test -n "$(PLATFORMS)" || { \
		echo "make -C images: PLATFORMS is empty - name what to publish (e.g. linux/amd64 linux/arm64)." >&2; \
		exit 1; }
	@for p in $(PLATFORMS); do \
		case "$$p" in \
			*/*) ;; \
			*) echo "make -C images: PLATFORMS entry '$$p' is not <os>/<arch> (e.g. linux/amd64)." >&2; exit 1;; \
		esac; \
	done

build: build-ssh build-jupyter ## Build both images

build-ssh: check-tag check-platform ## Build the self-authored ssh image
	$(CONTAINER_TOOL) build --platform "$(PLATFORM)" $(BASE_ARGS) $(APT_MIRROR_ARG) --build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		-f ssh-ubuntu-server/Dockerfile -t $(IMG_SSH) .

build-jupyter: check-tag check-platform ## Build the jupyter image
	$(CONTAINER_TOOL) build --platform "$(PLATFORM)" $(BASE_ARGS) $(APT_MIRROR_ARG) $(PIP_INDEX_URL_ARG) \
		-f jupyter/Dockerfile -t $(IMG_JUPYTER) .

smoke: build ## Build and run the full local Docker smoke
	IMG_SSH=$(IMG_SSH) IMG_JUPYTER=$(IMG_JUPYTER) hack/smoke.sh

smoke-ssh: build-ssh ## Build and smoke only the self-authored ssh image
	IMG_SSH=$(IMG_SSH) IMG_JUPYTER=$(IMG_JUPYTER) hack/smoke.sh --ssh

smoke-jupyter: build-jupyter ## Build and smoke only the jupyter image
	IMG_SSH=$(IMG_SSH) IMG_JUPYTER=$(IMG_JUPYTER) hack/smoke.sh --jupyter

push: push-ssh push-jupyter ## Publish both images as multi-arch indexes (TAG + :latest)

push-ssh: check-tag check-platforms ## Publish the ssh image as a multi-arch index (TAG + :latest)
	$(CONTAINER_TOOL) buildx build $(PLATFORMS_ARG) $(BUILDX_ATTEST) $(BASE_ARGS) $(APT_MIRROR_ARG) \
		--build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		-f ssh-ubuntu-server/Dockerfile -t $(IMG_SSH) -t $(LATEST_SSH) --push .

push-jupyter: check-tag check-platforms ## Publish the jupyter image as a multi-arch index (TAG + :latest)
	$(CONTAINER_TOOL) buildx build $(PLATFORMS_ARG) $(BUILDX_ATTEST) $(BASE_ARGS) $(APT_MIRROR_ARG) $(PIP_INDEX_URL_ARG) \
		-f jupyter/Dockerfile -t $(IMG_JUPYTER) -t $(LATEST_JUPYTER) --push .

help: ## Show this help message
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
