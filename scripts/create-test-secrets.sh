#!/bin/bash

set -euo pipefail

TMPDIR=$(mktemp -d)

readonly SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly REPO_ROOT="${SCRIPT_DIR}/.."
readonly CLUSTERCTL="${REPO_ROOT}/bin/clusterctl"
readonly WORKLOAD_CLUSTER="acceptance-workload-cluster"
readonly SECRET_NAME="$WORKLOAD_CLUSTER-kubeconfig"

clusterctl get kubeconfig $WORKLOAD_CLUSTER > "$HOME/.kube/workload-cluster.yaml"


kubectl --kubeconfig="$HOME/.kube/workload-cluster.yaml" apply -f https://docs.projectcalico.org/v3.21/manifests/calico.yaml
kubectl --kubeconfig="$HOME/.kube/workload-cluster.yaml" create namespace giantswarm || true

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: "$SECRET_NAME"
  namespace: "giantswarm"
type: Opaque
data:
  value: $( cat "$HOME/.kube/workload-cluster.yaml" | base64 | tr -d '\n' )
EOF

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: "gcp-credentials"
  namespace: "giantswarm"
type: Opaque
data:
  file.json: $( cat ${SCRIPT_DIR}/assets/test-credentials.json | base64 | tr -d '\n' )
EOF


# Point the kubeconfig to the exposed port of the load balancer, rather than the inaccessible container IP.
sed -i -e "s/server:.*/server: https:\/\/$(docker port ${WORKLOAD_CLUSTER}-lb 6443/tcp | sed "s/0.0.0.0/127.0.0.1/")/g" "$HOME/.kube/workload-cluster.yaml"

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

kubectl --kubeconfig="$HOME/.kube/workload-cluster.yaml" apply -f "${SCRIPT_DIR}/assets/cluster-issuer.yaml"

cat <<EOF | kubectl --kubeconfig="$HOME/.kube/workload-cluster.yaml" apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: "gcp-credentials"
  namespace: "giantswarm"
type: Opaque
data:
  file.json: $( cat ${SCRIPT_DIR}/assets/test-credentials.json | base64 | tr -d '\n' )
EOF

