#!/bin/sh
set -eu

repository=${DRUN_REPO:-abcdlsj/drun}
version=${DRUN_VERSION:-latest}
install_dir=${DRUN_INSTALL_DIR:-/usr/local/bin}

case $(uname -s) in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *)
    printf 'drun: unsupported operating system: %s\n' "$(uname -s)" >&2
    exit 1
    ;;
esac

case $(uname -m) in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *)
    printf 'drun: unsupported architecture: %s\n' "$(uname -m)" >&2
    exit 1
    ;;
esac

archive="drun_${os}_${arch}.tar.gz"
if [ -n "${DRUN_DOWNLOAD_BASE:-}" ]; then
  download_base=${DRUN_DOWNLOAD_BASE%/}
elif [ "$version" = latest ]; then
  download_base="https://github.com/${repository}/releases/latest/download"
else
  case "$version" in
    v*) ;;
    *) version="v${version}" ;;
  esac
  download_base="https://github.com/${repository}/releases/download/${version}"
fi

temp_dir=$(mktemp -d "${TMPDIR:-/tmp}/drun-install.XXXXXX")
cleanup() {
  rm -rf "$temp_dir"
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

download() {
  source_url=$1
  destination=$2
  if command -v curl >/dev/null 2>&1; then
    curl --fail --location --silent --show-error "$source_url" --output "$destination"
  elif command -v wget >/dev/null 2>&1; then
    wget --quiet --output-document="$destination" "$source_url"
  else
    printf 'drun: curl or wget is required\n' >&2
    exit 1
  fi
}

printf 'Downloading drun for %s/%s...\n' "$os" "$arch"
download "$download_base/$archive" "$temp_dir/$archive"
download "$download_base/checksums.txt" "$temp_dir/checksums.txt"

expected_checksum=$(awk -v archive="$archive" '$2 == archive { print $1 }' "$temp_dir/checksums.txt")
if [ -z "$expected_checksum" ]; then
  printf 'drun: checksum for %s was not found\n' "$archive" >&2
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual_checksum=$(sha256sum "$temp_dir/$archive" | awk '{ print $1 }')
elif command -v shasum >/dev/null 2>&1; then
  actual_checksum=$(shasum -a 256 "$temp_dir/$archive" | awk '{ print $1 }')
else
  printf 'drun: sha256sum or shasum is required\n' >&2
  exit 1
fi

if [ "$actual_checksum" != "$expected_checksum" ]; then
  printf 'drun: checksum verification failed for %s\n' "$archive" >&2
  exit 1
fi

tar -xzf "$temp_dir/$archive" -C "$temp_dir" drun

if [ -d "$install_dir" ] && [ -w "$install_dir" ]; then
  install -m 0755 "$temp_dir/drun" "$install_dir/drun"
elif command -v sudo >/dev/null 2>&1; then
  sudo install -d "$install_dir"
  sudo install -m 0755 "$temp_dir/drun" "$install_dir/drun"
else
  printf 'drun: %s is not writable; set DRUN_INSTALL_DIR to a writable directory\n' "$install_dir" >&2
  exit 1
fi

printf 'Installed %s\n' "$install_dir/drun"
"$install_dir/drun" --version
