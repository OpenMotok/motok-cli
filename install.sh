#!/bin/sh
set -eu

version=${NMODEL_VERSION:-v0.1.0}
bin_dir=${NMODEL_BIN_DIR:-${HOME:?HOME is required}/.local/bin}
release_url=${NMODEL_RELEASE_BASE_URL:-}
from_dir=
usage() {
  cat <<'HELP'
Install the NModel CLI binary (no Go or Node.js required).

  sh install.sh --from-dir ./dist --version v0.1.0
  NMODEL_RELEASE_BASE_URL=https://your-release-host/v0.1.0 sh install.sh --version v0.1.0

Options: --version VERSION --bin-dir DIRECTORY --from-dir DIRECTORY --help
Environment: NMODEL_VERSION, NMODEL_BIN_DIR, NMODEL_RELEASE_BASE_URL
Remote URLs must use HTTPS. Release artifacts are checked against SHA256SUMS.
Windows users should extract the matching release ZIP and add nmodel.exe to PATH.
HELP
}
while [ "$#" -gt 0 ]; do
  case "$1" in
    --help|-h) usage; exit 0 ;;
    --version|--bin-dir|--from-dir)
      if [ "$#" -lt 2 ] || [ -z "$2" ]; then echo 'Option requires a value.' >&2; exit 1; fi
      case "$1" in --version) version=$2 ;; --bin-dir) bin_dir=$2 ;; --from-dir) from_dir=$2 ;; esac
      shift 2 ;;
    *) echo 'Unknown installer option; use --help.' >&2; exit 1 ;;
  esac
done
case "$version" in ''|*[!A-Za-z0-9._-]*) echo 'Invalid version.' >&2; exit 1 ;; esac
case "$(uname -s)" in Darwin) target_os=darwin ;; Linux) target_os=linux ;; *) echo 'Unsupported OS; use a release archive.' >&2; exit 1 ;; esac
case "$(uname -m)" in x86_64|amd64) target_arch=amd64 ;; arm64|aarch64) target_arch=arm64 ;; *) echo 'Unsupported CPU architecture.' >&2; exit 1 ;; esac
archive="nmodel_${version}_${target_os}_${target_arch}.tar.gz"
install_tmp=$(mktemp -d "${TMPDIR:-/tmp}/nmodel-install.XXXXXX")
staged_binary=
cleanup() {
  if [ -n "$staged_binary" ]; then rm -f "$staged_binary"; fi
  rm -rf "$install_tmp"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM
if [ -n "$from_dir" ]; then
  cp "$from_dir/$archive" "$install_tmp/$archive"
  cp "$from_dir/SHA256SUMS" "$install_tmp/SHA256SUMS"
else
  case "$release_url" in https://*) ;; *) echo 'Set NMODEL_RELEASE_BASE_URL to an HTTPS release directory, or use --from-dir.' >&2; exit 1 ;; esac
  command -v curl >/dev/null 2>&1 || { echo 'curl is required to download releases.' >&2; exit 1; }
  release_url=${release_url%/}
  curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    "$release_url/$archive" -o "$install_tmp/$archive"
  curl --fail --silent --show-error --location --proto '=https' --proto-redir '=https' \
    "$release_url/SHA256SUMS" -o "$install_tmp/SHA256SUMS"
fi
expected=$(awk -v file="$archive" '$2 == file { print $1 }' "$install_tmp/SHA256SUMS")
case "$expected" in ''|*[!A-Fa-f0-9]*) echo 'Invalid checksum manifest.' >&2; exit 1 ;; esac
[ "${#expected}" -eq 64 ] || { echo 'Missing or duplicate artifact checksum.' >&2; exit 1; }
if command -v sha256sum >/dev/null 2>&1; then
  actual=$(sha256sum "$install_tmp/$archive" | awk '{print $1}')
else
  command -v shasum >/dev/null 2>&1 || { echo 'A SHA-256 utility is required.' >&2; exit 1; }
  actual=$(shasum -a 256 "$install_tmp/$archive" | awk '{print $1}')
fi
expected=$(printf '%s' "$expected" | tr 'A-F' 'a-f')
[ "$actual" = "$expected" ] || { echo 'Checksum mismatch; installation aborted.' >&2; exit 1; }
mkdir "$install_tmp/unpack"
tar -xzf "$install_tmp/$archive" -C "$install_tmp/unpack" nmodel
[ -f "$install_tmp/unpack/nmodel" ] && [ ! -L "$install_tmp/unpack/nmodel" ] || { echo 'Invalid binary archive.' >&2; exit 1; }
case "$bin_dir" in /*) ;; *) bin_dir="$(pwd)/$bin_dir" ;; esac
mkdir -p "$bin_dir"
staged_binary=$(mktemp "$bin_dir/.nmodel.XXXXXX")
cp "$install_tmp/unpack/nmodel" "$staged_binary"
chmod 755 "$staged_binary"
mv -f "$staged_binary" "$bin_dir/nmodel"
staged_binary=
echo "Installed nmodel $version in $bin_dir."
case ":${PATH:-}:" in *":$bin_dir:"*) ;; *) echo 'Add this directory to PATH to run nmodel.' ;; esac
