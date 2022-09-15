#!/bin/bash

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly REPO_ROOT="${SCRIPT_DIR}/.."
readonly CLUSTER=${CLUSTER:-"acceptance"}
readonly KIND="${REPO_ROOT}/bin/kind"
readonly CLUSTERCTL="${REPO_ROOT}/bin/clusterctl"
readonly IMG=${IMG:-quay.io/giantswarm/workload-identity-operator-gcp:dev}
readonly WORKLOAD_CLUSTER="acceptance-workload-cluster"

ensure_kind_cluster() {
  local cluster
  cluster="$1"
  if ! "$KIND" get clusters | grep -q "$cluster"; then
    export CLUSTER_TOPOLOGY=true

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
  --namespace cert-manager \
  --create-namespace \
  --version v1.8.0 \
  --set installCRDs=true \
  --wait

$KUBECTL apply -f "${SCRIPT_DIR}/assets/cluster-issuer.yaml"

export CLUSTER_TOPOLOGY=true
clusterctl init --infrastructure docker || true

echo "---> Waiting for CAPI controller deployments to be ready"

set -x

$KUBECTL -n "capd-system" rollout status "deploy/capd-controller-manager"
$KUBECTL -n capi-kubeadm-bootstrap-system rollout status deploy/capi-kubeadm-bootstrap-controller-manager
$KUBECTL -n capi-kubeadm-control-plane-system rollout status deploy/capi-kubeadm-control-plane-controller-manager
$KUBECTL -n capi-system rollout status deploy/capi-controller-manager

{ set +x; } 2>/dev/null

$KUBECTL apply -f "${SCRIPT_DIR}/assets/workload-cluster.yaml"

set -x
is_control_plane_ready=$($KUBECTL get kubeadmcontrolplane -o jsonpath='{.items[*].status.initialized}')
while [ "$is_control_plane_ready" != "True" ]; do
  echo "Waiting for control plane"
  is_control_plane_ready=$($KUBECTL get kubeadmcontrolplane.controlplane.cluster.x-k8s.io/controlplane -o jsonpath='{..status.conditions[?(@.type=="Ready")].status}')
  sleep 5
done

{ set +x; } 2>/dev/null

"$KIND" load docker-image --name "$WORKLOAD_CLUSTER" "$IMG"
