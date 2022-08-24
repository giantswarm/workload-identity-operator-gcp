#!/bin/bash

set -euo pipefail

TMPDIR=$(mktemp -d)

readonly SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly REPO_ROOT="${SCRIPT_DIR}/.."
readonly KIND="${REPO_ROOT}/bin/kind"
readonly CLUSTER_API_GCP_UPSTREAM=${CLUSTER_API_GCP_UPSTREAM:-"https://github.com/kubernetes-sigs/cluster-api-provider-gcp/raw/main/config/crd/bases/infrastructure.cluster.x-k8s.io_gcpclusters.yaml"}

"$KIND" export kubeconfig --name "$CLUSTER" --kubeconfig "$HOME/.kube/$CLUSTER.yml"

curl -sL -o ${TMPDIR}/crd.yaml ${CLUSTER_API_GCP_UPSTREAM}

kubectl apply -f "${TMPDIR}/crd.yaml"