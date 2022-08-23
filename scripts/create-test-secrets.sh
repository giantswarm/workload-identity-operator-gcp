#!/bin/bash

set -euo pipefail

readonly SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
readonly SECRET_NAME="$CLUSTER-kubeconfig"

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: "$SECRET_NAME"
  namespace: "giantswarm"
type: Opaque
data:
  value: $( cat ${HOME}/.kube/$CLUSTER.yml | base64 | tr -d '\n' )
EOF

cat <<EOF | kubectl apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: "gcp-credentials"
  namespace: "giantswarm"
type: Opaque
data:
  file.json: $( cat ${SCRIPT_DIR}/assets/test_creds.json | base64 | tr -d '\n' )
EOF

