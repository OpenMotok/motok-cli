#!/bin/sh
set -eu

version=${1:-v0.1.0}
case "$version" in ''|*[!A-Za-z0-9._-]*) echo 'Invalid version.' >&2; exit 1 ;; esac
project_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"
mkdir -p dist
build_tmp=$(mktemp -d "${TMPDIR:-/tmp}/nmodel-release.XXXXXX")
trap 'rm -rf "$build_tmp"' EXIT HUP INT TERM

for target in darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64 windows/arm64; do
  target_os=${target%/*}
  target_arch=${target#*/}
  name="nmodel_${version}_${target_os}_${target_arch}"
  stage="$build_tmp/$name"
  mkdir -p "$stage"
  binary=nmodel
  if [ "$target_os" = windows ]; then binary=nmodel.exe; fi
  CGO_ENABLED=0 GOOS="$target_os" GOARCH="$target_arch" go build -trimpath \
    -ldflags="-s -w -X nmodel.ai/cli/internal/cli.Version=${version#v}" \
    -o "$stage/$binary" ./cmd/nmodel
  if [ "$target_os" = windows ]; then
    (cd "$stage" && zip -q "$build_tmp/$name.zip" "$binary")
    mv -f "$build_tmp/$name.zip" "dist/$name.zip"
  else
    tar -czf "dist/$name.tar.gz" -C "$stage" "$binary"
  fi
  echo "Built $name"
done

checksum_tmp="$build_tmp/SHA256SUMS"
: > "$checksum_tmp"
for target in darwin_amd64 darwin_arm64 linux_amd64 linux_arm64 windows_amd64 windows_arm64; do
  extension=tar.gz
  case "$target" in windows_*) extension=zip ;; esac
  archive="nmodel_${version}_${target}.$extension"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd dist && sha256sum "$archive") >> "$checksum_tmp"
  else
    (cd dist && shasum -a 256 "$archive") >> "$checksum_tmp"
  fi
done
cp "$checksum_tmp" dist/SHA256SUMS
echo 'Release artifacts and SHA256SUMS are ready in dist/.'
