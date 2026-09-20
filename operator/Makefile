# Image URL to use all building/pushing image targets
IMG ?= harbor.isuanova.com/suanova/cubestack-controller-manager:latest
# YEAR defines the year value used for substituting the YEAR placeholder in the boilerplate header.
YEAR ?= $(shell date +%Y)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# CONTAINER_TOOL defines the container tool to be used for building images.
# Be aware that the target commands are only tested with Docker which is
# scaffolded by default. However, you might want to replace it to use other
# tools. (i.e. podman)
CONTAINER_TOOL ?= docker

# Setting SHELL to bash allows bash commands to be executed by recipes.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk command is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	"$(CONTROLLER_GEN)" object:headerFile="hack/boilerplate.go.txt",year=$(YEAR) paths="./..."

.PHONY: generate-check
generate-check: manifests generate ## Fail if generated code or manifests drift from the API sources.
	git diff --exit-code || { echo "generated files out of date — run: make manifests generate"; exit 1; }
	test -z "$$(git ls-files --others --exclude-standard -- config/crd/bases)" || { echo "untracked generated CRDs — run: make manifests generate and commit them"; exit 1; }

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet setup-envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell "$(ENVTEST)" use -i $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path)" go test $$(go list ./... | grep -v /e2e) -coverprofile cover.out

# TODO(user): To use a different vendor for e2e tests, modify the setup under 'tests/e2e'.
# The default setup assumes Kind is pre-installed and builds/loads the Manager Docker image locally.
# kubectl kuberc is disabled by default for test isolation; enable with:
# - KUBECTL_KUBERC=true
# CertManager is installed by default; skip with:
# - CERT_MANAGER_INSTALL_SKIP=true
KIND_CLUSTER ?= cubestack-test-e2e

.PHONY: setup-test-e2e
setup-test-e2e: ## Set up a Kind cluster for e2e tests if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { \
		echo "Kind is not installed. Please install Kind manually."; \
		exit 1; \
	}
	@case "$$($(KIND) get clusters)" in \
		*"$(KIND_CLUSTER)"*) \
			echo "Kind cluster '$(KIND_CLUSTER)' already exists. Skipping creation." ;; \
		*) \
			echo "Creating Kind cluster '$(KIND_CLUSTER)'..."; \
			$(KIND) create cluster --name $(KIND_CLUSTER) ;; \
	esac

.PHONY: test-e2e
test-e2e: setup-test-e2e manifests generate fmt vet ## Run the e2e tests. Expected an isolated environment using Kind.
	@status=0; \
	trap '$(MAKE) cleanup-test-e2e || true' EXIT; \
	KIND=$(KIND) KIND_CLUSTER=$(KIND_CLUSTER) go test -tags=e2e ./test/e2e/ -v -ginkgo.v || status=$$?; \
	exit $$status

.PHONY: cleanup-test-e2e
cleanup-test-e2e: ## Tear down the Kind cluster used for e2e tests
	@$(KIND) delete cluster --name $(KIND_CLUSTER)

# ---- Helm-based e2e (dedicated kind cluster, independent of test-e2e) ----
KIND_CLUSTER_HELM ?= cubestack-helm-e2e
# kubectl/helm pinned to the dedicated cluster regardless of the ambient
# kubeconfig context.
HELM_E2E_KUBECTL = $(KUBECTL) --context kind-$(KIND_CLUSTER_HELM)
HELM_E2E_HELM = $(HELM) --kube-context kind-$(KIND_CLUSTER_HELM)
HELM_NAMESPACE ?= cubestack-system
# Envoy Gateway version whose CRDs the helm e2e installs. The chart does not
# ship an EnvoyProxy — the dataplane belongs to the EnvoyProxy the GatewayClass
# references, which the platform owns — but the CRDs are part of the same
# install a real cluster would have (README, Prerequisites), so the kind cluster
# gets them and a platform-style class-level EnvoyProxy would be accepted here.
# Pinned because it is v1alpha1 and churns between EG minors; v1.9.1 is the
# version the chart README verifies ListenerSet reconciliation against.
ENVOY_GATEWAY_VERSION ?= v1.9.1
# Where the Envoy Gateway controller — and, with it, the class-level EnvoyProxy
# the e2e applies — lives. The chart's gateway.dataplaneNamespace defaults to
# this same namespace, and the manager reads the dataplane Service from there to
# decide how an endpoint is addressed: it looks for a NodePort dataplane and
# renumbers every port onto its nodePort. The e2e configures a LoadBalancer one
# instead, so the manager finds none and publishes each port as itself.
ENVOY_GATEWAY_NAMESPACE ?= envoy-gateway-system
# Envoy Gateway's chart, controller image, and the proxy image its dataplane
# runs — served from the platform registry rather than Docker Hub. The runner
# reaches harbor.isuanova.com on the internal network, and Docker Hub's
# anonymous pull limits are a CI failure this stays clear of. Repos follow the
# naming already in that project, <upstream-org>-<image>.
#
# The controller's tag is ENVOY_GATEWAY_VERSION itself, so chart and controller
# cannot disagree. The proxy's is NOT derivable from it: Envoy Gateway compiles
# the envoy build it ships with into its binary and publishes the pairing only
# in its release compatibility matrix, so it is pinned here next to the version
# it belongs to and has to be re-checked whenever that moves.
#
# All three are set to a mirrored copy, so moving ENVOY_GATEWAY_VERSION means
# running hack/mirror-e2e-images.sh before the e2e can pass.
ENVOY_GATEWAY_CHART ?= oci://harbor.isuanova.com/suanova/envoyproxy-gateway-helm-chart
ENVOY_GATEWAY_IMAGE ?= harbor.isuanova.com/suanova/envoyproxy-gateway:$(ENVOY_GATEWAY_VERSION)
ENVOY_PROXY_IMAGE ?= harbor.isuanova.com/suanova/envoyproxy-envoy:distroless-v1.39.1
# Split on the LAST colon so registries with a port (reg:5000/img:tag) work, as
# HELM_E2E_IMAGE_REPO/TAG below do.
ENVOY_GATEWAY_IMAGE_REPO = $(shell echo '$(ENVOY_GATEWAY_IMAGE)' | sed -E 's/:[^:]*$$//')
ENVOY_GATEWAY_IMAGE_TAG = $(shell echo '$(ENVOY_GATEWAY_IMAGE)' | sed -E 's/^.*://')
HELM_CHART_DIR ?= helm/cubestack-controller-manager-chart
ECHO_IMG ?= example.com/cubestack-e2e-echo:v0.0.1
HELM_RELEASE ?= cubestack
# --- DevEnvironment image the e2e environment runs ---
# coordinates images/Makefile publishes under; TAG there also defaults to the
# short commit SHA, so the two agree without being wired together.
DEV_ENV_IMAGE ?= harbor.isuanova.com/suanova/jupyter-minimal:$(shell git rev-parse --short HEAD)
# The platform the kind node runs. A kind node matches the docker daemon's
# architecture, which on an arm64 laptop is not the amd64 these images publish
# as — and an image loaded for the wrong one fails at container start with an
# exec format error, not at load. Override to build for another platform
# deliberately.
DEV_ENV_PLATFORM ?= linux/$(shell uname -m | sed -e 's/x86_64/amd64/' -e 's/aarch64/arm64/')
# permissionInitImage in internal/controller/assets.go: the hardcoded busybox the
# workspace-ownership init container runs. Read out of the source rather than
# restated, so a change there cannot leave this loading a reference nothing pulls:
# the init container fails closed, so the environment would never start.
PERMISSION_INIT_IMAGE ?= $(shell sed -n 's/^[[:space:]]*permissionInitImage[[:space:]]*=[[:space:]]*"\(.*\)"$$/\1/p' internal/controller/assets.go)
# What the images below are built FROM, as the platform registry's mirrored copy
# and the name the Dockerfile asks for. They are pulled from the mirror and
# re-tagged under that second name, so `docker build` resolves the base from the
# local store instead of Docker Hub and GCR — verified: with a tag present
# locally the build never contacts a registry, and without one it fails trying.
# Re-tagging rather than threading a --build-arg through the Dockerfiles keeps
# product files out of a CI concern. A base bumped in a Dockerfile but not here
# still builds correctly; it just pulls upstream again.
BUILD_BASE_MIRRORS = harbor.isuanova.com/suanova/jupyter-minimal-notebook:2026-09-07=quay.io/jupyter/minimal-notebook:2026-09-07 harbor.isuanova.com/suanova/golang:1.26=golang:1.26 harbor.isuanova.com/suanova/distroless-static:nonroot=gcr.io/distroless/static:nonroot harbor.isuanova.com/suanova/python:3.12-alpine=python:3.12-alpine
# Manager image for the helm e2e flow. Defaults to the same registry image as
# the top-of-file IMG default; the chart Deployment template bakes
# imagePullPolicy=IfNotPresent, so the kind-loaded image wins over the
# registry even for the :latest tag. Derives from IMG when the caller sets
# IMG explicitly (command line or environment).
ifeq ($(origin IMG),file)
HELM_E2E_IMG ?= harbor.isuanova.com/suanova/cubestack-controller-manager:latest
else
HELM_E2E_IMG ?= $(IMG)
endif
# Split on the LAST colon so registries with a port (reg:5000/img:tag) work.
HELM_E2E_IMAGE_REPO = $(shell echo '$(HELM_E2E_IMG)' | sed -E 's/:[^:]*$$//')
HELM_E2E_IMAGE_TAG = $(shell echo '$(HELM_E2E_IMG)' | sed -E 's/^.*://')

.PHONY: helm-e2e-setup
helm-e2e-setup: ## Create the dedicated kind cluster for helm e2e if it does not exist
	@command -v $(KIND) >/dev/null 2>&1 || { echo "Kind is not installed. Please install Kind manually."; exit 1; }
	@if $(KIND) get clusters | grep -Fxq "$(KIND_CLUSTER_HELM)"; then \
		echo "Kind cluster '$(KIND_CLUSTER_HELM)' already exists. Skipping creation."; \
	else \
		echo "Creating Kind cluster '$(KIND_CLUSTER_HELM)'..."; \
		$(KIND) create cluster --name $(KIND_CLUSTER_HELM); \
	fi
	@echo "Installing gateway-api CRDs (the manager watches Gateway/HTTPRoute/TCPRoute)..." ; \
	GW_VER="$$(awk '$$1=="sigs.k8s.io/gateway-api" {print $$2}' go.mod)"; \
	[ -n "$$GW_VER" ] || { echo "sigs.k8s.io/gateway-api not found in go.mod"; exit 1; }; \
	go mod download sigs.k8s.io/gateway-api; \
	GW_CRD_DIR="$$(go env GOMODCACHE)/sigs.k8s.io/gateway-api@$${GW_VER}/config/crd/standard"; \
	[ -d "$$GW_CRD_DIR" ] || { echo "gateway-api CRD dir not found: $${GW_CRD_DIR}"; exit 1; }; \
	$(HELM_E2E_KUBECTL) apply -f "$${GW_CRD_DIR}"
	# The Envoy Gateway bundle also carries the gateway-api CRDs, and installing
	# those here is denied by gateway-api's own safe-upgrades VAP: the step above
	# already installed the standard channel from the pinned go module. Keep only
	# the group the chart's objects belong to.
	# --server-side is required, not stylistic: the envoyproxies and
	# securitypolicies CRDs are too big for client-side apply's 256KB
	# last-applied-configuration annotation.
	@echo "Installing Envoy Gateway CRDs (the platform prerequisite's API surface)..." ; \
	$(HELM) show crds $(ENVOY_GATEWAY_CHART) --version $(ENVOY_GATEWAY_VERSION) \
		| awk 'BEGIN { buf = "---\n" } /^---[[:space:]]*$$/ { if (keep) printf "%s", buf; buf = "---\n"; keep = 0; next } \
			{ buf = buf $$0 "\n"; if ($$0 == "  group: gateway.envoyproxy.io") keep = 1 } \
			END { if (keep) printf "%s", buf }' \
		| $(HELM_E2E_KUBECTL) apply --server-side -f -
	@echo "Provisioning the upstream LeaderWorkerSet controller (image from the pinned lws go module)..." ; \
	KIND_CLUSTER_HELM="$(KIND_CLUSTER_HELM)" KIND="$(KIND)" KUBECTL="$(KUBECTL)" hack/install-lws-controller.sh
	# The CRDs above are the API surface; this is the controller that programs
	# the Gateway. Without it the chart's Gateway is accepted by the API server
	# and never served, so nothing an environment publishes is reachable.
	# --skip-crds is required: the chart's crds/ also carries the gateway-api
	# CRDs installed from the pinned go module above, and re-applying those is
	# denied by gateway-api's own safe-upgrades ValidatingAdmissionPolicy.
	@echo "Installing the Envoy Gateway controller for the chart's Gateway..." ; \
	$(HELM) upgrade --install eg $(ENVOY_GATEWAY_CHART) \
		--version $(ENVOY_GATEWAY_VERSION) \
		--set deployment.envoyGateway.image.repository=$(ENVOY_GATEWAY_IMAGE_REPO) \
		--set deployment.envoyGateway.image.tag=$(ENVOY_GATEWAY_IMAGE_TAG) \
		--kube-context kind-$(KIND_CLUSTER_HELM) \
		--namespace $(ENVOY_GATEWAY_NAMESPACE) --create-namespace \
		--skip-crds --wait --timeout 5m ; \
	$(HELM_E2E_KUBECTL) rollout status deployment/envoy-gateway -n $(ENVOY_GATEWAY_NAMESPACE) --timeout=3m
	# The class that says what the dataplane is. A GatewayClass with no
	# parametersRef still works, but gets Envoy Gateway's built-in defaults; the
	# e2e points it at the EnvoyProxy above so the proxy Service is a
	# LoadBalancer MetalLB can address. Waits for Accepted=True, which is where a
	# missing or malformed parametersRef surfaces rather than at the first
	# unreachable endpoint.
	@echo "Applying the GatewayClass + EnvoyProxy prerequisite..." ; \
	sed "s#__ENVOY_PROXY_IMAGE__#$(ENVOY_PROXY_IMAGE)#" test/e2e/assets/envoyproxy-gatewayclass.yaml \
		| $(HELM_E2E_KUBECTL) apply -f - && \
	$(HELM_E2E_KUBECTL) wait --for=condition=Accepted gatewayclass/eg --timeout=2m
	# kind ships no cephfs; this gives the name the controller hardcodes to a
	# provisioner kind already has, configured for the ReadWriteMany the
	# workspace claim asks for. See the script for why a StorageClass alone is
	# not enough.
	@echo "Creating the workspace StorageClass..." ; \
	KIND_CLUSTER_HELM="$(KIND_CLUSTER_HELM)" KUBECTL="$(KUBECTL)" \
		hack/install-workspace-storageclass.sh
	@echo "Installing MetalLB (the address the Gateway's dataplane is reachable on)..." ; \
	KIND_CLUSTER_HELM="$(KIND_CLUSTER_HELM)" KUBECTL="$(KUBECTL)" DOCKER="$(CONTAINER_TOOL)" hack/install-metallb.sh

.PHONY: helm-e2e-images
helm-e2e-images: ## Build and load manager, echo and DevEnvironment base images into the helm kind cluster
	# Has to run before the first build: these are what those builds are FROM.
	@for pair in $(BUILD_BASE_MIRRORS); do \
		src="$${pair%%=*}"; dst="$${pair#*=}"; \
		if $(CONTAINER_TOOL) image inspect "$${dst}" >/dev/null 2>&1; then \
			echo "$${dst} is already present locally — not pulling $${src}"; \
			continue; \
		fi; \
		echo "Pulling $${src} and re-tagging it as $${dst}..."; \
		$(CONTAINER_TOOL) pull --platform $(DEV_ENV_PLATFORM) "$${src}" && \
		$(CONTAINER_TOOL) tag "$${src}" "$${dst}" || exit 1; \
	done
	$(MAKE) docker-build IMG=$(HELM_E2E_IMG)
	@if [ -f test/e2e/assets/echo/Dockerfile ]; then \
		docker build -f test/e2e/assets/echo/Dockerfile -t $(ECHO_IMG) test/e2e/assets/echo; \
		$(KIND) load docker-image $(ECHO_IMG) --name $(KIND_CLUSTER_HELM); \
	else \
		echo "echo image not present yet (created by a later task) — skipping echo build/load"; \
	fi
	$(KIND) load docker-image $(HELM_E2E_IMG) --name $(KIND_CLUSTER_HELM)
	# The environment under test runs a real image out of images/, so the e2e
	# exercises the images the platform actually ships rather than a stand-in.
	# Only jupyter-minimal: the environment is a jupyter one, and that image
	# serves the ssh endpoint too (its entrypoint starts sshd when the operator
	# mounts the ssh material), so ssh-ubuntu22.04 has nothing here to run in.
	@echo "Building the DevEnvironment base image..." ; \
	$(MAKE) -C ../images build-jupyter PLATFORM=$(DEV_ENV_PLATFORM) IMG_JUPYTER=$(DEV_ENV_IMAGE)
	$(KIND) load docker-image $(DEV_ENV_IMAGE) --name $(KIND_CLUSTER_HELM)
	# The init container's image is hardcoded in the controller, so the tag has to
	# be the one it names — including the platform registry host, which does not
	# resolve on the cluster. Pulling that name straight from the mirror is what
	# keeps the environment off a registry it cannot reach; upstream busybox
	# carries the same tag, so no re-tagging is needed. No ImagePullPolicy is set
	# on the container, so a non-:latest tag resolves to IfNotPresent and the
	# loaded image wins.
	@[ -n "$(PERMISSION_INIT_IMAGE)" ] || { echo "permissionInitImage not found in internal/controller/assets.go"; exit 1; } ; \
	echo "Loading $(PERMISSION_INIT_IMAGE) (busybox, for the workspace init container)..." ; \
	$(CONTAINER_TOOL) pull --platform $(DEV_ENV_PLATFORM) $(PERMISSION_INIT_IMAGE) && \
	$(KIND) load docker-image $(PERMISSION_INIT_IMAGE) --name $(KIND_CLUSTER_HELM)

.PHONY: helm-e2e-install
helm-e2e-install: helm-e2e-setup helm-e2e-images manifests helm-crds-sync generate fmt vet ## Install the operator via helm into the dedicated kind cluster
	$(HELM_E2E_HELM) upgrade --install $(HELM_RELEASE) $(HELM_CHART_DIR) \
		--namespace $(HELM_NAMESPACE) --create-namespace \
		--set image.repository=$(HELM_E2E_IMAGE_REPO) \
		--set image.tag=$(HELM_E2E_IMAGE_TAG) \
		--wait --timeout 5m
	@echo "manager rollout status:"
	$(HELM_E2E_KUBECTL) rollout status deployment/cubestack-controller-manager -n $(HELM_NAMESPACE) --timeout=3m

.PHONY: helm-e2e-crd-check
helm-e2e-crd-check: ## Verify chart CRDs plus the prerequisite lws/gateway CRDs, VAPs and RBAC (phase-1 acceptance)
	@set -e; \
	echo "CRDs:"; $(HELM_E2E_KUBECTL) get crd | grep -E "ai\.cubestack\.io|leaderworkerset\.x-k8s\.io|disaggregatedset\.x-k8s\.io"; \
	for crd in devenvironments.ai.cubestack.io inferenceruntimeprofiles.ai.cubestack.io inferenceservices.ai.cubestack.io modelversions.ai.cubestack.io leaderworkersets.leaderworkerset.x-k8s.io disaggregatedsets.disaggregatedset.x-k8s.io disaggregatedsetrolescalers.disaggregatedset.x-k8s.io; do \
		$(HELM_E2E_KUBECTL) get crd $$crd >/dev/null || { echo "MISSING CRD: $$crd"; exit 1; }; \
	done; \
	echo "VAPs:"; $(HELM_E2E_KUBECTL) get validatingadmissionpolicy | grep -E "modelversions|inferenceservices|inferenceruntimeprofiles"; \
	echo "VAP bindings:"; $(HELM_E2E_KUBECTL) get validatingadmissionpolicybinding | grep -E "modelversions|inferenceservices|inferenceruntimeprofiles"; \
	echo "RBAC:"; $(HELM_E2E_KUBECTL) get clusterrole cubestack-manager-role >/dev/null; \
	$(HELM_E2E_KUBECTL) get clusterrole cubestack-metrics-reader >/dev/null; \
	$(HELM_E2E_KUBECTL) get clusterrolebinding cubestack-manager-rolebinding >/dev/null; \
	$(HELM_E2E_KUBECTL) get sa cubestack-controller-manager -n $(HELM_NAMESPACE) >/dev/null; \
	$(HELM_E2E_KUBECTL) auth can-i update devenvironments.ai.cubestack.io \
		--as=system:serviceaccount:$(HELM_NAMESPACE):cubestack-controller-manager -n $(HELM_NAMESPACE) | grep -qx yes \
		|| { echo "manager SA cannot update DevEnvironments (the controller adds and removes its finalizer with Update)"; exit 1; }; \
	echo "platform Gateway:"; \
	$(HELM_E2E_KUBECTL) get gateway cubestack-gateway -n $(HELM_NAMESPACE) >/dev/null \
		|| { echo "MISSING: the chart did not create Gateway/cubestack-gateway"; exit 1; }
	# The chart creates the Gateway and nothing else. No dataplane object may
	# live here: a Gateway carrying an EnvoyProxy of its own replaces its class's
	# provider config rather than merging with it, so a chart-shipped one would
	# silently override whatever the platform configured for the whole class.
	@set -e; \
	[ -z "$$($(HELM_E2E_KUBECTL) get gateway cubestack-gateway -n $(HELM_NAMESPACE) -o jsonpath='{.spec.infrastructure}')" ] \
		|| { echo "Gateway/cubestack-gateway carries spec.infrastructure: the dataplane belongs to the GatewayClass's EnvoyProxy"; exit 1; }; \
	[ -z "$$($(HELM_E2E_KUBECTL) get envoyproxy -n $(HELM_NAMESPACE) -o name 2>/dev/null)" ] \
		|| { echo "the release shipped an EnvoyProxy of its own — the dataplane belongs to the GatewayClass"; exit 1; }; \
	$(HELM_E2E_KUBECTL) get deploy cubestack-controller-manager -n $(HELM_NAMESPACE) -o jsonpath='{.spec.template.spec.containers[0].args}' \
		| grep -q -- "--gateway-name=cubestack-gateway" \
		|| { echo "the manager was not told the Gateway's name: gateway.name must reach --gateway-name, or the object above is not the one it publishes through"; exit 1; }; \
	$(HELM_E2E_KUBECTL) get deploy cubestack-controller-manager -n $(HELM_NAMESPACE) -o jsonpath='{.spec.template.spec.containers[0].args}' \
		| grep -q -- "--gateway-namespace=$(HELM_NAMESPACE)" \
		|| { echo "the manager was not told the release namespace: --gateway-namespace must render .Release.Namespace, or it looks for the Gateway in cubestack-system"; exit 1; }; \
	echo "schema validation (invalid MV must be rejected):"; \
	if printf '%s\n' 'apiVersion: ai.cubestack.io/v1alpha1' 'kind: ModelVersion' 'metadata:' '  name: e2e-invalid-should-fail' 'spec:' '  model: x' | $(HELM_E2E_KUBECTL) apply -f -; then \
		echo "ERROR: invalid ModelVersion was accepted — CRD schema not enforced"; \
		exit 1; \
	fi
	@echo "helm-e2e-crd-check: OK"

.PHONY: helm-e2e-verify
helm-e2e-verify: helm-e2e-setup helm-e2e-install ## Full gpu-less happy path: ISVC to Ready=True with rendered-arg assertion
	@set -e; \
	KIND_NODE="$$($(KIND) get nodes --name $(KIND_CLUSTER_HELM) 2>/dev/null || echo $(KIND_CLUSTER_HELM)-control-plane)"; \
	echo "pre-creating the HostPath model directory on the kind node (HostPathDirectory)"; \
	docker exec $$KIND_NODE mkdir -p /mnt/cubestack-e2e-model; \
	$(HELM_E2E_KUBECTL) create ns demo --dry-run=client -o yaml | $(HELM_E2E_KUBECTL) apply -f - >/dev/null; \
	$(HELM_E2E_KUBECTL) apply -f test/e2e/assets/modelversion.yaml >/dev/null; \
	$(HELM_E2E_KUBECTL) apply -f test/e2e/assets/inferenceruntimeprofile.yaml >/dev/null; \
	$(HELM_E2E_KUBECTL) apply -f test/e2e/assets/inferenceservice.yaml >/dev/null; \
	echo "waiting for the rendered override to reach the pod logs (up to 4m)..."; \
	# Poll-before-wait is deliberate: the log poll guarantees a pod from this \
	# spec exists before kubectl wait, which would otherwise early-return on a \
	# stale Ready=True from a previous spec. Do not reorder. \
	POD=""; \
	for i in $$(seq 1 80); do \
		for p in $$($(HELM_E2E_KUBECTL) get pods -n demo -l leaderworkerset.sigs.k8s.io/name=e2e-service-router -o jsonpath='{.items[*].metadata.name}' 2>/dev/null); do \
			if $(HELM_E2E_KUBECTL) logs -n demo $$p 2>/dev/null | grep -q "ECHO_ARGV:--verbosity=9"; then POD=$$p; break 2; fi; \
		done; \
		sleep 3; \
	done; \
	[ -n "$$POD" ] || { echo "rendered arg --verbosity=9 not found in any e2e-service-router pod log within 4m"; $(HELM_E2E_KUBECTL) get pods -n demo; exit 1; }; \
	echo "rendered arg asserted in pod $$POD"; \
	echo "waiting for ISVC Ready (gpu-less, up to 4m)..."; \
	$(HELM_E2E_KUBECTL) wait --for=condition=Ready inferenceservice/e2e-service -n demo --timeout=4m; \
	echo "condition dump:"; \
	$(HELM_E2E_KUBECTL) get inferenceservice e2e-service -n demo -o jsonpath='{range .status.conditions[*]}{.type}={.status} {end}'; echo; \
	echo "asserting hostPath volume mount:"; \
	$(HELM_E2E_KUBECTL) get pod -n demo $$POD -o jsonpath='{range .spec.volumes[*]}{.name}={.hostPath.path}{"\n"}{end}' | grep -q "/mnt/cubestack-e2e-model" || { echo "hostPath volume missing"; exit 1; }; \
	$(HELM_E2E_KUBECTL) get endpoints -n demo e2e-service-router -o jsonpath='{.subsets[0].addresses}' | grep -q . || { echo "no ready endpoints"; exit 1; }; \
	echo "helm-e2e-verify: OK"

# Takes no prerequisites, like helm-e2e-crd-check: it asserts against a cluster a
# previous target built. Depending on helm-e2e-install here would re-run setup,
# the image builds and the chart install a third time in the one CI job that runs
# all of them.
.PHONY: helm-e2e-devenv-verify
helm-e2e-devenv-verify: ## Create a real DevEnvironment and assert Jupyter and SSH answer through the Gateway's VIP
	KIND_CLUSTER_HELM="$(KIND_CLUSTER_HELM)" KUBECTL="$(KUBECTL)" DEV_ENV_IMAGE="$(DEV_ENV_IMAGE)" \
		hack/verify-devenv.sh

.PHONY: helm-e2e-uninstall
helm-e2e-uninstall: ## Uninstall the helm release; CRDs intentionally remain
	$(HELM_E2E_HELM) uninstall $(HELM_RELEASE) --namespace $(HELM_NAMESPACE)

.PHONY: helm-e2e-cleanup
helm-e2e-cleanup: ## Delete the dedicated kind cluster
	@$(KIND) delete cluster --name $(KIND_CLUSTER_HELM)

.PHONY: helm-crds-sync
helm-crds-sync: ## Copy the ai.cubestack.io CRDs from config/crd/bases into the chart (crds/ is not committed)
	rm -rf $(HELM_CHART_DIR)/crds
	mkdir -p $(HELM_CHART_DIR)/crds
	cp config/crd/bases/*.yaml $(HELM_CHART_DIR)/crds/

.PHONY: helm-resources-update
helm-resources-update: ## Regenerate chart resources from config/ sources
	hack/update-helm-resources.sh

.PHONY: helm-resources-check
helm-resources-check: ## Fail if chart resources are out of sync with config/ sources
	hack/update-helm-resources.sh && git diff --exit-code -- helm hack/update-helm-resources.sh || { echo "chart resources out of date — run: make helm-resources-update"; exit 1; }

.PHONY: helm-package
helm-package: helm-resources-update helm-crds-sync ## Package the chart as a tgz next to the chart (distributable artifact)
	# helm package embeds any pre-existing *.tgz it finds in the chart dir —
	# drop stale archives first so re-runs stay clean and deterministic.
	rm -f $(HELM_CHART_DIR)/*.tgz
	$(HELM) package $(HELM_CHART_DIR) --destination $(HELM_CHART_DIR)

.PHONY: lint
lint: golangci-lint ## Run golangci-lint linter
	"$(GOLANGCI_LINT)" run

.PHONY: lint-fix
lint-fix: golangci-lint ## Run golangci-lint linter and perform fixes
	"$(GOLANGCI_LINT)" run --fix

.PHONY: lint-config
lint-config: golangci-lint ## Verify golangci-lint linter configuration
	"$(GOLANGCI_LINT)" config verify

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build manager binary.
	go build -o bin/manager cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./cmd/main.go

# If you wish to build the manager image targeting other platforms you can use the --platform flag.
# (i.e. docker build --platform linux/arm64). However, you must enable docker buildKit for it.
# More info: https://docs.docker.com/develop/develop-images/build_enhancements/
.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	$(CONTAINER_TOOL) build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	$(CONTAINER_TOOL) push ${IMG}

# PLATFORMS defines the target platforms for the manager image be built to provide support to multiple
# architectures. (i.e. make docker-buildx IMG=myregistry/mypoperator:0.0.1). To use this option you need to:
# - be able to use docker buildx. More info: https://docs.docker.com/build/buildx/
# - have enabled BuildKit. More info: https://docs.docker.com/develop/develop-images/build_enhancements/
# - be able to push the image to your registry (i.e. if you do not set a valid value via IMG=<myregistry/image:<tag>> then the export will fail)
# To adequately provide solutions that are compatible with multiple platforms, you should consider using this option.
PLATFORMS ?= linux/arm64,linux/amd64,linux/s390x,linux/ppc64le
.PHONY: docker-buildx
docker-buildx: ## Build and push docker image for the manager for cross-platform support
	# copy existing Dockerfile and insert --platform=${BUILDPLATFORM} into Dockerfile.cross, and preserve the original Dockerfile
	sed -e '1 s/\(^FROM\)/FROM --platform=\$$\{BUILDPLATFORM\}/; t' -e ' 1,// s//FROM --platform=\$$\{BUILDPLATFORM\}/' Dockerfile > Dockerfile.cross
	builder="cubestack-builder-$$$$"; \
	trap '$(CONTAINER_TOOL) buildx rm "$$builder" >/dev/null 2>&1 || true; rm -f Dockerfile.cross' EXIT; \
	$(CONTAINER_TOOL) buildx create --name "$$builder" --use; \
	$(CONTAINER_TOOL) buildx build --push --platform=$(PLATFORMS) --tag ${IMG} -f Dockerfile.cross .

.PHONY: build-installer
build-installer: manifests generate kustomize ## Generate a consolidated YAML with CRDs and deployment.
	mkdir -p dist
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default > dist/install.yaml

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: install
install: manifests kustomize ## Install CRDs into the K8s cluster specified in ~/.kube/config.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" apply -f -; else echo "No CRDs to install; skipping."; fi

.PHONY: uninstall
uninstall: manifests kustomize ## Uninstall CRDs from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	@out="$$( "$(KUSTOMIZE)" build config/crd 2>/dev/null || true )"; \
	if [ -n "$$out" ]; then echo "$$out" | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -; else echo "No CRDs to delete; skipping."; fi

.PHONY: deploy
deploy: manifests kustomize ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	cd config/manager && "$(KUSTOMIZE)" edit set image controller=${IMG}
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" apply -f -

.PHONY: undeploy
undeploy: kustomize ## Undeploy controller from the K8s cluster specified in ~/.kube/config. Call with ignore-not-found=true to ignore resource not found errors during deletion.
	"$(KUSTOMIZE)" build config/default | "$(KUBECTL)" delete --ignore-not-found=$(ignore-not-found) -f -

##@ Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p "$(LOCALBIN)"

## Tool Binaries
KUBECTL ?= kubectl
KIND ?= kind
HELM ?= helm
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GOLANGCI_LINT = $(LOCALBIN)/golangci-lint

## Tool Versions
KUSTOMIZE_VERSION ?= v5.8.1
CONTROLLER_TOOLS_VERSION ?= v0.21.0

#ENVTEST_VERSION is the controller-runtime version to use for setup-envtest, derived from go.mod
ENVTEST_VERSION ?= $(shell v='$(call gomodver,sigs.k8s.io/controller-runtime)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_VERSION manually (controller-runtime replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v")

#ENVTEST_K8S_VERSION is the version of Kubernetes to use for setting up ENVTEST binaries (i.e. 1.31)
ENVTEST_K8S_VERSION ?= $(shell v='$(call gomodver,k8s.io/api)'; \
  [ -n "$$v" ] || { echo "Set ENVTEST_K8S_VERSION manually (k8s.io/api replace has no tag)" >&2; exit 1; }; \
  printf '%s\n' "$$v" | sed -E 's/^v?[0-9]+\.([0-9]+).*/1.\1/')

GOLANGCI_LINT_VERSION ?= v2.12.2
.PHONY: kustomize
kustomize: $(KUSTOMIZE) ## Download kustomize locally if necessary.
$(KUSTOMIZE): $(LOCALBIN)
	$(call go-install-tool,$(KUSTOMIZE),sigs.k8s.io/kustomize/kustomize/v5,$(KUSTOMIZE_VERSION))

.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	$(call go-install-tool,$(CONTROLLER_GEN),sigs.k8s.io/controller-tools/cmd/controller-gen,$(CONTROLLER_TOOLS_VERSION))

.PHONY: setup-envtest
setup-envtest: envtest ## Download the binaries required for ENVTEST in the local bin directory.
	@if [ -z "$$(ls -d "$(LOCALBIN)"/k8s/* 2>/dev/null)" ]; then \
		echo "Setting up envtest binaries for Kubernetes version $(ENVTEST_K8S_VERSION)..."; \
		"$(ENVTEST)" use $(ENVTEST_K8S_VERSION) --bin-dir "$(LOCALBIN)" -p path || { \
			echo "Error: Failed to set up envtest binaries for version $(ENVTEST_K8S_VERSION)."; \
			exit 1; \
		}; \
	fi

.PHONY: envtest
envtest: $(ENVTEST) ## Download setup-envtest locally if necessary.
$(ENVTEST): $(LOCALBIN)
	$(call go-install-tool,$(ENVTEST),sigs.k8s.io/controller-runtime/tools/setup-envtest,$(ENVTEST_VERSION))

.PHONY: golangci-lint
golangci-lint: $(GOLANGCI_LINT) ## Download golangci-lint locally if necessary.

# vanilla golangci-lint binary, installed once per version
$(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION):
	@mkdir -p "$(LOCALBIN)"
	$(call go-install-tool,$(GOLANGCI_LINT),github.com/golangci/golangci-lint/v2/cmd/golangci-lint,$(GOLANGCI_LINT_VERSION))

# golangci-lint with module plugins, built once per version (or when .custom-gcl.yml changes).
# Without .custom-gcl.yml this is a symlink to the vanilla binary.
$(LOCALBIN)/golangci-lint-custom-$(GOLANGCI_LINT_VERSION): $(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION) $(if $(wildcard .custom-gcl.yml),.custom-gcl.yml,)
	@if [ -f .custom-gcl.yml ]; then \
		echo "Building custom golangci-lint with plugins..."; \
		"$(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)" custom --destination $(LOCALBIN) --name golangci-lint-custom; \
		mv -f $(LOCALBIN)/golangci-lint-custom $@; \
	else \
		ln -sf "$(LOCALBIN)/golangci-lint-$(GOLANGCI_LINT_VERSION)" $@; \
	fi

$(GOLANGCI_LINT): $(LOCALBIN)/golangci-lint-custom-$(GOLANGCI_LINT_VERSION)
	@ln -sf "$<" "$(GOLANGCI_LINT)"

# go-install-tool will 'go install' any package with custom target and name of binary, if it doesn't exist
# $1 - target path with name of binary
# $2 - package url which can be installed
# $3 - specific version of package
define go-install-tool
@[ -f "$(1)-$(3)" ] && [ "$$(readlink -- "$(1)" 2>/dev/null)" = "$(1)-$(3)" ] || { \
set -e; \
package=$(2)@$(3) ;\
echo "Downloading $${package}" ;\
rm -f "$(1)" ;\
GOBIN="$(LOCALBIN)" go install $${package} ;\
mv "$(LOCALBIN)/$$(basename "$(1)")" "$(1)-$(3)" ;\
} ;\
ln -sf "$$(realpath "$(1)-$(3)")" "$(1)"
endef

define gomodver
$(shell go list -m -f '{{if .Replace}}{{.Replace.Version}}{{else}}{{.Version}}{{end}}' $(1) 2>/dev/null)
endef
