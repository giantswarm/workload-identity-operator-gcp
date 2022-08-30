#!/bin/bash

set -euo pipefail

curl -L https://github.com/kubernetes-sigs/cluster-api/releases/download/v1.2.1/clusterctl-linux-amd64 -o clusterctl
chmod +x ./clusterctl
sudo mv ./clusterctl /usr/local/bin/clusterctl
