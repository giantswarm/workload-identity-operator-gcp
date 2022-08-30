#!/bin/bash

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly REPO_ROOT="${SCRIPT_DIR}/.."
readonly CLUSTER=${CLUSTER:-"acceptance"}
readonly KIND="${REPO_ROOT}/bin/kind"
readonly CLUSTERCTL="${REPO_ROOT}/bin/clusterctl"
readonly IMG=${IMG:-quay.io/giantswarm/workload-identity-operator-gcp:latest}
readonly WORKLOAD_CLUSTER="acceptance-workload-cluster"

ensure_kind_cluster() {
  local cluster
  cluster="$1"
  if ! "$KIND" get clusters | grep -q "$cluster"; then
    export CLUSTER_TOPOLOGY=true

    "$KIND" create cluster --name "$cluster" --config "${SCRIPT_DIR}/assets/kind-cluster-with-extramounts.yaml" --wait 5m
  fi
  "$KIND" export kubeconfig --name "$cluster" --kubeconfig "$HOME/.kube/$cluster.yml"
}

ensure_kind_cluster "$CLUSTER"
kubectl create namespace giantswarm --kubeconfig "$HOME/.kube/$CLUSTER.yml" || true
"$KIND" load docker-image --name "$CLUSTER" "$IMG"

helm repo add jetstack https://charts.jetstack.io
helm repo update
helm upgrade --install \
  cert-manager jetstack/cert-manager \
  --namespace cert-manager \
  --create-namespace \
  --version v1.8.0 \
  --set installCRDs=true \
  --wait

kubectl apply -f "${SCRIPT_DIR}/assets/cluster-issuer.yaml"

# "$CLUSTERCTL" init --infrastructure docker
clusterctl init --infrastructure docker

echo "---> Waiting for CAPI controller deployments to be ready"

set -x

kubectl -n "capd-system" rollout status "deploy/capd-controller-manager"
kubectl -n capi-kubeadm-bootstrap-system rollout status deploy/capi-kubeadm-bootstrap-controller-manager
kubectl -n capi-kubeadm-control-plane-system rollout status deploy/capi-kubeadm-control-plane-controller-manager
kubectl -n capi-system rollout status deploy/capi-controller-manager

{ set +x; } 2> /dev/null

kubectl apply -f "${SCRIPT_DIR}/assets/workload-cluster.yaml"

"$KIND" load docker-image --name "$WORKLOAD_CLUSTER" "$IMG"

