# Images workspace Makefile — build, smoke & publish the DevEnvironment base images.
# Build context is this directory (see AGENTS.md). Each image has one name: build and
# smoke work on one platform in the local store, and push publishes that name plus
# :latest as a single multi-arch index (PLATFORMS); PUSH_LATEST=0 withholds :latest,
# and retag-latest moves it afterwards for a caller that has to decide later. buildx
# cannot load locally and push in one step, so the published layers are a fresh build
# of the same source rather than the very image the local smoke accepted.

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

# Base-image build args, defaulting to the platform registry's mirrored copies of the upstream
# bases. Building from the platform's copy is what keeps the published image descended from
# the base the platform serves, and it is the only copy that resolves where upstream is
# unreachable. They live in the shared MIRROR_PROJECT, each under its upstream reference with
# the upstream host as the first path segment — the same rule hack/mirror-e2e-images.sh
# mirrors under, and the same refs operator/Makefile's BASE_MIRRORS names. Each is named by
# tag *and* digest, and the digest is what the build resolves:
# the mirrored tag is movable — operator/hack/mirror-e2e-images.sh repoints it when someone
# here re-mirrors — so on the tag alone one commit could publish different base layers on two
# days under the same TAG, which is the reproducibility that TAG is supposed to carry. The
# digest is the mirror's index digest, so each platform still resolves its own manifest and
# PLATFORMS is not narrowed. Bumping a base is deliberate: read the new digest, update it here
# and in operator/Makefile's BASE_MIRRORS (the map the e2e's own builds and the mirror script
# read — the two must name the same bases), then re-run the mirror script, which fails if a
# mirrored tag no longer hashes to the digest it is listed under. Pass `BASE_ARGS=` to resolve
# FROM upstream instead, unpinned. The jupyter date tracks the base in jupyter/Dockerfile; bump
# both together.
BASE_ARGS ?= \
	--build-arg JUPYTER_BASE=$(REGISTRY)/$(MIRROR_PROJECT)/quay.io/jupyter/minimal-notebook:2026-09-07@sha256:f6dc79ec57e05d28fa8f08dbd457c0d46f79e3677083e2eed9fb2d892942d810 \
	--build-arg UBUNTU_BASE=$(UBUNTU_BASE)

# Base release of the self-authored ssh image. It selects the build base AND names the
# published repository (decision doc §5), so the name cannot outlive the base. The release is
# one axis for both: it is looked up in UBUNTU_BASE_DIGESTS below, and nothing else selects
# the base, so a release and its digest can never be chosen apart.
UBUNTU_VERSION ?= 22.04

# The pinned base, one entry per supported Ubuntu release, as <version>=<digest of the
# platform registry's copy>. A release with no entry has no base to build from: its tag would
# carry another release's digest, which the build resolves — so it would either fail the pull
# or build a release the published name does not claim. `make build-ssh UBUNTU_VERSION=24.04`
# stops in check-ubuntu-base instead, until 24.04 is mirrored and pinned here.
UBUNTU_BASE_DIGESTS = 22.04=sha256:b8b6ee6aa931ecd9d0d952abc34dc0e5f7c6a30c6bb71b079fe399fde0329c02

UBUNTU_BASE_DIGEST = $(patsubst $(UBUNTU_VERSION)=%,%,$(filter $(UBUNTU_VERSION)=%,$(UBUNTU_BASE_DIGESTS)))
UBUNTU_BASE = $(REGISTRY)/$(MIRROR_PROJECT)/docker.io/library/ubuntu:$(UBUNTU_VERSION)@$(UBUNTU_BASE_DIGEST)

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
# Where the third-party bases the images are built FROM are mirrored. A project of its
# own rather than PROJECT, which names the images this workspace publishes: the bases are
# shared platform-wide — the e2e's builds and this one resolve the same refs — and the
# project is not this workspace's to own.
MIRROR_PROJECT ?= mirrors
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

# Whether `make push` also moves :latest. A caller that may not move it — CI, which
# only moves :latest when the run's commit is still the head of main — passes
# PUSH_LATEST=0 and calls `make retag-latest` once it knows: buildx cannot load
# locally and push in one step, so asking this Makefile for both would rebuild the
# whole index a second time. See .github/workflows/ci-images.yml.
PUSH_LATEST ?= 1

LATEST_ARG_SSH     = $(if $(filter 1,$(PUSH_LATEST)),-t $(LATEST_SSH))
LATEST_ARG_JUPYTER = $(if $(filter 1,$(PUSH_LATEST)),-t $(LATEST_JUPYTER))

.PHONY: help check-tag check-platform check-platforms check-push-latest check-ubuntu-base build build-ssh build-jupyter \
	smoke smoke-ssh smoke-jupyter push push-ssh push-jupyter retag-latest

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

# LATEST_ARG_* read any value other than 1 as "do not move :latest", which is the
# wrong answer for the one a human reaches for: PUSH_LATEST=true would publish and
# silently leave :latest behind. Name the two accepted values instead.
check-push-latest:
	@case "$(PUSH_LATEST)" in \
		1 | 0) ;; \
		*) echo "make -C images: PUSH_LATEST is '$(PUSH_LATEST)' - expected 1 (also move :latest) or 0 (leave it)." >&2; \
		   exit 1;; \
	esac

# UBUNTU_VERSION reaches this repository's name *and* the base it is built from, so a release
# misspelled or not yet pinned would be published under a name claiming a release it does not
# contain. Only the defaulted BASE_ARGS is guarded — that one is this file's own pin, and the
# lookup is what has to answer. A caller-supplied BASE_ARGS is the caller's own base, pinned or
# upstream (`BASE_ARGS=`) as they chose, and this file has nothing to check it against.
check-ubuntu-base:
	@test "$(origin BASE_ARGS)" != file || test -n "$(UBUNTU_BASE_DIGEST)" || { \
		echo "make -C images: UBUNTU_VERSION=$(UBUNTU_VERSION) has no pinned base in UBUNTU_BASE_DIGESTS." >&2; \
		echo "                Add '<version>=sha256:<digest>' for the mirror's copy of that release, and" >&2; \
		echo "                re-run operator/hack/mirror-e2e-images.sh if it is not mirrored yet." >&2; \
		echo "                Or pass BASE_ARGS= to build FROM upstream, unpinned." >&2; \
		exit 1; }

build: build-ssh build-jupyter ## Build both images

build-ssh: check-tag check-platform check-ubuntu-base ## Build the self-authored ssh image
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

push-ssh: check-tag check-platforms check-push-latest check-ubuntu-base ## Publish the ssh image as a multi-arch index (TAG, and :latest unless PUSH_LATEST=0)
	$(CONTAINER_TOOL) buildx build $(PLATFORMS_ARG) $(BUILDX_ATTEST) $(BASE_ARGS) $(APT_MIRROR_ARG) \
		--build-arg UBUNTU_VERSION=$(UBUNTU_VERSION) \
		-f ssh-ubuntu-server/Dockerfile -t $(IMG_SSH) $(LATEST_ARG_SSH) --push .

push-jupyter: check-tag check-platforms check-push-latest ## Publish the jupyter image as a multi-arch index (TAG, and :latest unless PUSH_LATEST=0)
	$(CONTAINER_TOOL) buildx build $(PLATFORMS_ARG) $(BUILDX_ATTEST) $(BASE_ARGS) $(APT_MIRROR_ARG) $(PIP_INDEX_URL_ARG) \
		-f jupyter/Dockerfile -t $(IMG_JUPYTER) $(LATEST_ARG_JUPYTER) --push .

# Repoint :latest at a TAG already in the registry rather than rebuilding: imagetools
# writes an index naming the same manifests, so this cannot publish anything the build
# did not produce. It is how a caller that withheld :latest from `push` moves it after
# deciding to. Needs no PLATFORMS: nothing is built.
retag-latest: check-tag ## Repoint :latest at an already-published TAG (no rebuild)
	$(CONTAINER_TOOL) buildx imagetools create -t $(LATEST_SSH) $(IMG_SSH)
	$(CONTAINER_TOOL) buildx imagetools create -t $(LATEST_JUPYTER) $(IMG_JUPYTER)

help: ## Show this help message
	@grep -E '^[a-zA-Z0-9_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'
