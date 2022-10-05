#!/bin/bash

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly REPO_ROOT="${SCRIPT_DIR}/.."
readonly CLUSTER=${CLUSTER:-"acceptance"}
readonly KIND="${REPO_ROOT}/bin/kind"
readonly CLUSTERCTL="${REPO_ROOT}/bin/clusterctl"
readonly IMG=${IMG:-quay.io/giantswarm/workload-identity-operator-gcp:dev}

ensure_kind_cluster() {
  local cluster
  cluster="$1"
  if ! "$KIND" get clusters | grep -q "$cluster"; then
    "$KIND" create cluster --name "$cluster" --image kindest/node:v1.22.0 --config "${SCRIPT_DIR}/assets/kind-cluster-with-extramounts.yaml" --wait 5m
  fi
  "$KIND" export kubeconfig --name "$cluster" --kubeconfig "$HOME/.kube/$cluster.yml"
}

ensure_kind_cluster "$CLUSTER"
KUBECTL="kubectl --kubeconfig=$HOME/.kube/$CLUSTER.yml"

$KUBECTL create namespace giantswarm --kubeconfig "$HOME/.kube/$CLUSTER.yml" || true
"$KIND" load docker-image --name "$CLUSTER" "$IMG"

helm repo add jetstack https://charts.jetstack.io
helm repo update
helm upgrade --install \
  cert-manager jetstack/cert-manager \
  --kubeconfig="$HOME/.kube/$CLUSTER.yml" \
  --namespace cert-manager \
  --create-namespace \
  --version v1.8.0 \
  --set installCRDs=true \
  --wait

$KUBECTL apply -f "${SCRIPT_DIR}/assets/cluster-issuer.yaml"

