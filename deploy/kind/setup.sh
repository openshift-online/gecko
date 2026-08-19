#!/usr/bin/env bash
set -euo pipefail

CLUSTER_NAME="gecko-dev"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# Use podman or docker, whichever is available.
if command -v podman &>/dev/null; then
  BUILDER=podman
elif command -v docker &>/dev/null; then
  BUILDER=docker
else
  echo "Error: neither podman nor docker found" >&2
  exit 1
fi

# Create the kind cluster if it does not already exist.
if kind get clusters 2>/dev/null | grep -qx "$CLUSTER_NAME"; then
  echo "==> kind cluster '$CLUSTER_NAME' already exists, skipping creation."
else
  echo "==> Creating kind cluster '$CLUSTER_NAME'..."
  kind create cluster --name "$CLUSTER_NAME"
fi

# Ensure kubectl is targeting the right cluster before touching anything.
EXPECTED_CTX="kind-${CLUSTER_NAME}"
CURRENT_CTX="$(kubectl config current-context 2>/dev/null || true)"
if [[ "$CURRENT_CTX" != "$EXPECTED_CTX" ]]; then
  echo "Error: current kubectl context is '${CURRENT_CTX:-<none>}', expected '$EXPECTED_CTX'" >&2
  echo "Run: kubectl config use-context $EXPECTED_CTX" >&2
  exit 1
fi

echo "==> Installing cert-manager..."
# Pin to a reviewed cert-manager release version for security and reproducibility.
# v1.14.0 has a known broken cainjector OCI image; use v1.14.7 (latest 1.14 patch).
CERTMGR_VERSION="v1.14.7"
CERTMGR_URL="https://github.com/cert-manager/cert-manager/releases/download/$CERTMGR_VERSION/cert-manager.yaml"

# Download cert-manager manifest.
CERTMGR_MANIFEST=$(mktemp /tmp/cert-manager-XXXXXX.yaml)
trap "rm -f $CERTMGR_MANIFEST" EXIT

if ! curl -fsSL --retry 3 "$CERTMGR_URL" -o "$CERTMGR_MANIFEST"; then
  echo "Error: failed to download cert-manager manifest" >&2
  exit 1
fi

kubectl apply -f "$CERTMGR_MANIFEST"
echo "==> Waiting for cert-manager webhook..."
kubectl -n cert-manager rollout status deploy/cert-manager-webhook --timeout=120s

echo "==> Creating self-signed ClusterIssuer..."
# The webhook can take a few seconds after the rollout reports ready.
for i in 1 2 3 4 5; do
  kubectl apply -f "$SCRIPT_DIR/clusterissuer.yaml" 2>/dev/null && break
  echo "    waiting for cert-manager webhook (attempt $i)..."
  sleep 5
done

load_image() {
  local image="$1"
  if [[ "$BUILDER" == "podman" ]]; then
    # kind load docker-image uses the Docker daemon; when building with podman
    # we must save to a tar archive and use kind load image-archive instead.
    local tmptar
    tmptar="$(mktemp /tmp/gecko-image-XXXXXX.tar)"
    podman save -o "$tmptar" "$image"
    kind load image-archive "$tmptar" --name "$CLUSTER_NAME"
    rm -f "$tmptar"
  else
    kind load docker-image "$image" --name "$CLUSTER_NAME"
  fi
}

# Determine the target architecture for the kind node.
KIND_ARCH="$(kubectl get node "$CLUSTER_NAME-control-plane" -o jsonpath='{.status.nodeInfo.architecture}' 2>/dev/null || echo amd64)"

echo "==> Building platform-api-server binary (host-native, target linux/$KIND_ARCH)..."
(cd "$REPO_ROOT/platform-api" && CGO_ENABLED=0 GOOS=linux GOARCH="$KIND_ARCH" \
  go build -o "$REPO_ROOT/_output/platform-api-server" ./cmd/platform-api-server)

echo "==> Building container image..."
$BUILDER build -f "$SCRIPT_DIR/Containerfile" \
  -t localhost/platform-api-server:latest "$REPO_ROOT/_output"
echo "==> Loading platform-api-server into kind cluster '$CLUSTER_NAME'..."
load_image localhost/platform-api-server:latest

echo "==> Deploying platform-api-server..."
kubectl apply -k "$SCRIPT_DIR"

echo "==> Restarting deployment to pick up new image..."
kubectl -n gecko-system rollout restart deploy/platform-api-server

echo "==> Waiting for platform-api-server..."
kubectl -n gecko-system rollout status deploy/platform-api-server --timeout=120s

echo "==> Verifying API registration..."
kubectl get apiservice v1.gcp.managed.openshift.io

echo "==> Creating system platform roles..."
kubectl apply -f - <<'EOF'
apiVersion: gcp.managed.openshift.io/v1
kind: PlatformRole
metadata:
  name: cluster-viewer
spec:
  permissions:
    - cluster.list
    - cluster.get
    - nodepool.list
    - nodepool.get
  system: true
---
apiVersion: gcp.managed.openshift.io/v1
kind: PlatformRole
metadata:
  name: cluster-admin
spec:
  permissions:
    - cluster.create
    - cluster.list
    - cluster.get
    - cluster.update
    - cluster.delete
    - nodepool.create
    - nodepool.list
    - nodepool.get
    - nodepool.update
    - nodepool.delete
  system: true
---
apiVersion: gcp.managed.openshift.io/v1
kind: PlatformRole
metadata:
  name: service-admin
spec:
  permissions:
    - rolebinding.create
    - rolebinding.list
    - rolebinding.get
    - rolebinding.update
    - rolebinding.delete
    - role.create
    - role.list
    - role.get
    - role.update
    - role.delete
  system: true
EOF

echo "==> Verifying platform roles..."
kubectl get platformroles.gcp.managed.openshift.io

echo ""
echo "Done. The cluster is running — see deploy/kind/README.md for testing instructions."
echo ""
echo "Quick start:"
echo "  kubectl -n gecko-system port-forward svc/platform-api-server-public 8081:8081 &"
