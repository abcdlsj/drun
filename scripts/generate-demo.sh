#!/usr/bin/env bash
# Records assets/demo.gif by driving the real drun binary against scripts/demo/docker.
set -euo pipefail

repo_root=$(cd "$(dirname "$0")/.." && pwd)
command -v vhs >/dev/null || { echo "vhs is required: brew install vhs" >&2; exit 1; }

work_dir=$(mktemp -d "${TMPDIR:-/tmp}/drun-demo.XXXXXX")
trap 'rm -rf "$work_dir"' EXIT
mkdir -p "$work_dir/bin" "$work_dir/state"

cd "$repo_root"
go build -o "$work_dir/bin/drun" .
cp scripts/demo/docker "$work_dir/bin/docker"

DRUN_DEMO_BIN="$work_dir/bin" DRUN_DEMO_STATE="$work_dir/state" vhs scripts/demo/demo.tape
