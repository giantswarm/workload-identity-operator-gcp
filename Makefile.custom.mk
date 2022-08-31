
# Image URL to use all building/pushing image targets
IMG ?= quay.io/giantswarm/workload-identity-operator-gcp:dev

# Substitute colon with space - this creates a list.
# Word selects the n-th element of the list
IMAGE_REPO = $(word 1,$(subst :, ,$(IMG)))
IMAGE_TAG = $(word 2,$(subst :, ,$(IMG)))

CLUSTER ?= acceptance
# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.23

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Setting SHELL to bash allows bash commands to be executed by recipes.
# This is a requirement for 'setup-envtest.sh' in the test target.
# Options are set to exit when a recipe line exits non-zero or a piped command fails.
SHELL = /usr/bin/env bash -o pipefail
.SHELLFLAGS = -ec

.PHONY: all
all: build

##@ General

# The help target prints out all targets with their descriptions organized
# beneath their categories. The categories are represented by '##@' and the
# target descriptions by '##'. The awk commands is responsible for reading the
# entire set of makefiles included in this invocation, looking for lines of the
# file as xyz: ## something, and then pretty-format the target and help. Then,
# if there's a line with ##@ something, that gets pretty-printed as a category.
# More info on the usage of ANSI control characters for terminal formatting:
# https://en.wikipedia.org/wiki/ANSI_escape_code#SGR_parameters
# More info on the awk command:
# http://linuxcommand.org/lc3_adv_awk.php

##@ Development

.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	$(CONTROLLER_GEN) rbac:roleName=manager-role crd webhook paths="./..." output:crd:artifacts:config=config/crd/bases

.PHONY: generate
generate: controller-gen ## Generate code containing DeepCopy, DeepCopyInto, and DeepCopyObject method implementations.
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

.PHONY: fmt
fmt: ## Run go fmt against code.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code.
	go vet ./...

.PHONY: lint-imports
lint-imports: goimports ## Run go vet against code.
	./scripts/check-imports.sh

.PHONY: create-acceptance-cluster
create-acceptance-cluster: kind
	CLUSTER=$(CLUSTER) IMG=$(IMG) ./scripts/ensure-kind-cluster.sh

.PHONY: deploy-capg-crds
deploy-capg-crds: kind
	KUBECONFIG="$(KUBECONFIG)" CLUSTER=$(CLUSTER) IMG=$(IMG) ./scripts/install-crds.sh

.PHONY: create-test-secrets
create-test-secrets: kind
	CLUSTER=$(CLUSTER) IMG=$(IMG) ./scripts/create-test-secrets.sh

.PHONY: deploy-acceptance-cluster
deploy-acceptance-cluster: docker-build create-acceptance-cluster deploy-capg-crds create-test-secrets deploy-on-workload-cluster deploy-capg-crds deploy-crds-on-workload deploy

.PHONY: deploy-crds-on-workload
deploy-crds-on-workload: kind
	KUBECONFIG="$(HOME)/.kube/workload-cluster.yaml" CLUSTER=$(CLUSTER) IMG=$(IMG) ./scripts/install-crds.sh

.PHONY: deploy-on-workload-cluster
deploy-on-workload-cluster: manifests render
	 helm upgrade --install \
	  --kubeconfig="$(HOME)/.kube/workload-cluster.yaml" \
		--namespace giantswarm \
		--set image.tag=$(IMAGE_TAG) \
		--set operationMode=onprem \
		--set credentials.name=gcp-credentials \
		--wait \
		workload-identity-operator-gcp helm/rendered/workload-identity-operator-gcp

.PHONY: test-unit
test-unit: ginkgo generate fmt vet envtest ## Run tests.
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) -p path)" $(GINKGO) -p --nodes 8 -r -randomize-all --randomize-suites --skip-package=tests ./...

.PHONY: test-acceptance
test-acceptance: KUBECONFIG=$(HOME)/.kube/$(CLUSTER).yml
test-acceptance: ginkgo deploy-acceptance-cluster  ## Run acceptance testst
	KUBECONFIG="$(KUBECONFIG)" $(GINKGO) -p --nodes 8 -r -randomize-all --randomize-suites tests/acceptance

.PHONY: test-all
test-all: lint lint-imports test-unit test-integration test-acceptance ## Run all tests and litner
##@ Build

.PHONY: build
build: generate fmt vet ## Build manager binary.
	go build -o bin/manager main.go

.PHONY: run
run: manifests generate fmt vet ## Run a controller from your host.
	go run ./main.go

.PHONY: docker-build
docker-build: ## Build docker image with the manager.
	docker build -t ${IMG} .

.PHONY: docker-push
docker-push: ## Push docker image with the manager.
	docker push ${IMG}

##@ Deployment

ifndef ignore-not-found
  ignore-not-found = false
endif

.PHONY: render
render: architect
	mkdir -p $(shell pwd)/helm/rendered
	cp -r $(shell pwd)/helm/workload-identity-operator-gcp $(shell pwd)/helm/rendered/
	$(ARCHITECT) helm template --dir $(shell pwd)/helm/rendered/workload-identity-operator-gcp

.PHONY: deploy
deploy: manifests render ## Deploy controller to the K8s cluster specified in ~/.kube/config.
	KUBECONFIG="$(KUBECONFIG)" helm upgrade --install \
		--namespace giantswarm \
		--set image.tag=$(IMAGE_TAG) \
		--set operationMode=onprem \
		--set credentials.name=gcp-credentials \
		--wait \
		workload-identity-operator-gcp helm/rendered/workload-identity-operator-gcp

.PHONY: undeploy
undeploy: ## Undeploy controller from the K8s  specified in ~/.kube/config.
	KUBECONFIG="$(KUBECONFIG)" helm uninstall \
		--namespace giantswarm \
		workload-identity-operator-gcp helm/rendered/workload-identity-operator-gcp


##@ Build Dependencies

## Location to install dependencies to
LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

## Tool Binaries
KUSTOMIZE ?= $(LOCALBIN)/kustomize
CONTROLLER_GEN ?= $(LOCALBIN)/controller-gen
ENVTEST ?= $(LOCALBIN)/setup-envtest
GINKGO ?= $(LOCALBIN)/ginkgo
ARCHITECT ?= $(LOCALBIN)/architect
KIND ?= $(LOCALBIN)/kind
GOIMPORTS ?= $(LOCALBIN)/goimports
CLUSTERCTL ?= $(LOCALBIN)/clusterctl

## Tool Versions
.PHONY: controller-gen
controller-gen: $(CONTROLLER_GEN) ## Download controller-gen locally if necessary.
$(CONTROLLER_GEN): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary.
$(ENVTEST): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

.PHONY: ginkgo
ginkgo: $(GINKGO) ## Download ginkgo locally if necessary.
$(GINKGO): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install github.com/onsi/ginkgo/v2/ginkgo@latest

.PHONY: architect
architect: $(ARCHITECT) ## Download architect locally if necessary.
$(ARCHITECT): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install github.com/giantswarm/architect@latest

.PHONY: kind
kind: $(KIND) ## Download kind locally if necessary.
$(KIND): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install sigs.k8s.io/kind@latest

.PHONY: goimports
goimports: $(GOIMPORTS) ## Download kind locally if necessary.
$(GOIMPORTS): $(LOCALBIN)
	GOBIN=$(LOCALBIN) go install golang.org/x/tools/cmd/goimports@latest

.PHONY: clusterctl
clusterctl: $(CLUSTERCTL) ## Download clusterctl locally if necessary.
$(CLUSTERCTL): $(LOCALBIN)
	$(eval LATEST_RELEASE = $(shell curl -s https://api.github.com/repos/kubernetes-sigs/cluster-api/releases/latest | jq -r '.tag_name'))
	curl -sL "https://github.com/kubernetes-sigs/cluster-api/releases/download/$(LATEST_RELEASE)/clusterctl-linux-amd64" -o $(CLUSTERCTL)
	chmod +x $(CLUSTERCTL)
