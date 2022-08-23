#!/bin/bash

set -euo pipefail

TMPDIR=$(mktemp -d)

readonly TEMP_CREDENTIALS_FILE="$(mktemp)"
readonly SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly REPO_ROOT="${SCRIPT_DIR}/.."
readonly KIND="${REPO_ROOT}/bin/kind"
readonly CLUSTERCTL="${REPO_ROOT}/bin/clusterctl"
readonly IMG=${IMG:-quay.io/giantswarm/workload-identity-operator-gcp:dev}
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

CREDENTIALS_FILE=${SCRIPT_DIR}/assets/test-credentials.json
if [ ! -f "$CREDENTIALS_FILE" ]; then
  B64_GOOGLE_APPLICATION_CREDENTIALS="${B64_GOOGLE_APPLICATION_CREDENTIALS:?Base64 encoded GCP credentials not exported}"
  echo $B64_GOOGLE_APPLICATION_CREDENTIALS | base64 -di >"$TEMP_CREDENTIALS_FILE"

  echo $TEMP_CREDENTIALS_FILE
  CREDENTIALS_FILE=$TEMP_CREDENTIALS_FILE
fi

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: "gcp-credentials"
  namespace: "giantswarm"
type: Opaque
data:
  file.json: $( cat $CREDENTIALS_FILE | base64 | tr -d '\n' )
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
  file.json: $( cat $CREDENTIALS_FILE | base64 | tr -d '\n' )
EOF

"$KIND" load docker-image --name "$WORKLOAD_CLUSTER" "$IMG"
