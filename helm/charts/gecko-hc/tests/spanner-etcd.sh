#!/usr/bin/env bash
set -euo pipefail

chart_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT

render() {
	helm template test "$chart_dir" --set image.tag=test "$@" > "$tmp_dir/rendered.yaml"
}

render
grep -q -- '--spanner-etcd-enabled=false' "$tmp_dir/rendered.yaml" || {
	echo 'Spanner-backed etcd must be disabled by default' >&2
	exit 1
}

render --set spannerEtcd.enabled=true
grep -q -- '--spanner-etcd-enabled=true' "$tmp_dir/rendered.yaml" || {
	echo 'spannerEtcd.enabled=true did not enable the controller flag' >&2
	exit 1
}

echo 'Spanner etcd chart tests passed.'
