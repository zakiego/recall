#!/usr/bin/env bash
# Downloads the official ONNX Runtime shared library that the embedder loads
# (internal/embed). The version is the one onnxruntime-node shipped in the
# TypeScript build this server replaced, so the goldens it wrote still hold. The SHA-256 of
# each archive is the digest GitHub publishes for the release asset.
#
#   scripts/fetch-onnxruntime.sh [os-arch] [dest]
#
# os-arch: darwin-arm64, linux-amd64 or linux-arm64 (default: this machine).
# dest: where libonnxruntime goes (default: lib/onnxruntime/<os-arch>).
# Prints the library path. Nothing is downloaded when it is already there.
set -euo pipefail

version="1.30.0"
here="$(cd "$(dirname "$0")/.." && pwd)"

target="${1:-}"
if [ -z "$target" ]; then
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
  target="$os-$arch"
fi

case "$target" in
  darwin-arm64)
    asset="onnxruntime-osx-arm64-$version.tgz"
    sha="6ebb5062a934537c352937821f9fe9718e7de1a2db1122a93dd363ffd53a7012"
    lib="libonnxruntime.$version.dylib"
    ;;
  linux-amd64)
    asset="onnxruntime-linux-x64-$version.tgz"
    sha="a5ed5a3cac51fbb2e90da632ae43d19212faaa20e76484e62bcb7c23ddb3b3fd"
    lib="libonnxruntime.so.$version"
    ;;
  linux-arm64)
    asset="onnxruntime-linux-aarch64-$version.tgz"
    sha="e16a27a8ed330bbc698df7330b0cf56e722f354e3bcc92118682c74ef3c3e3da"
    lib="libonnxruntime.so.$version"
    ;;
  *)
    echo "unsupported target: $target" >&2
    exit 1
    ;;
esac

dest="${2:-$here/lib/onnxruntime/$target}"
if [ -f "$dest/$lib" ]; then
  echo "$dest/$lib"
  exit 0
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
curl -fsSL -o "$tmp/$asset" "https://github.com/microsoft/onnxruntime/releases/download/v$version/$asset"
got="$(shasum -a 256 "$tmp/$asset" 2>/dev/null | cut -d' ' -f1 || sha256sum "$tmp/$asset" | cut -d' ' -f1)"
if [ "$got" != "$sha" ]; then
  echo "sha256 mismatch for $asset: got $got, want $sha" >&2
  exit 1
fi
tar -xzf "$tmp/$asset" -C "$tmp"
mkdir -p "$dest"
cp "$tmp/${asset%.tgz}/lib/$lib" "$dest/$lib"
echo "$dest/$lib"
