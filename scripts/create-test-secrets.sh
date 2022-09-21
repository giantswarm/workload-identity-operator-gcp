#!/bin/bash

set -euo pipefail

TMPDIR=$(mktemp -d)

readonly SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly REPO_ROOT="${SCRIPT_DIR}/.."
readonly KIND="${REPO_ROOT}/bin/kind"
readonly CLUSTERCTL="${REPO_ROOT}/bin/clusterctl"
readonly IMG=${IMG:-quay.io/giantswarm/workload-identity-operator-gcp:dev}
readonly WORKLOAD_CLUSTER="acceptance-workload-cluster"
readonly SECRET_NAME="$WORKLOAD_CLUSTER-kubeconfig"

clusterctl get kubeconfig -n $NAMESPACE $WORKLOAD_CLUSTER >"$HOME/.kube/workload-cluster.yaml"

KUBECTL="kubectl --kubeconfig=$HOME/.kube/workload-cluster.yaml"
$KUBECTL create namespace giantswarm || true

$KUBECTL apply -f https://docs.projectcalico.org/v3.21/manifests/calico.yaml

# Point the kubeconfig to the exposed port of the load balancer, rather than the inaccessible container IP.
# sed -i -e "s/server:.*/server: https:\/\/$(docker port ${WORKLOAD_CLUSTER}-lb 6443/tcp | sed "s/0.0.0.0/127.0.0.1/")/g" "$HOME/.kube/workload-cluster.yaml"

helm repo add jetstack https://charts.jetstack.io
helm repo update
helm upgrade --install \
  cert-manager jetstack/cert-manager \
  --kubeconfig="$HOME/.kube/workload-cluster.yaml" \
  --namespace cert-manager \
  --create-namespace \
  --version v1.8.0 \
  --set installCRDs=true \
  --wait

$KUBECTL apply -f "${SCRIPT_DIR}/assets/cluster-issuer.yaml"

"$KIND" load docker-image --name "$WORKLOAD_CLUSTER" "$IMG"
