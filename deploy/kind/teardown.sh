#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="gecko-dev"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

EXPECTED_CTX="kind-${CLUSTER_NAME}"
CURRENT_CTX="$(kubectl config current-context 2>/dev/null || true)"
if [[ "$CURRENT_CTX" != "$EXPECTED_CTX" ]]; then
  echo "Error: current kubectl context is '${CURRENT_CTX:-<none>}', expected '$EXPECTED_CTX'" >&2
  echo "Run: kubectl config use-context $EXPECTED_CTX" >&2
  exit 1
fi

echo "==> Removing deployed resources..."
kubectl delete -k "$SCRIPT_DIR" --ignore-not-found

echo "==> Deleting kind cluster '$CLUSTER_NAME'..."
kind delete cluster --name "$CLUSTER_NAME"

echo "Done."
