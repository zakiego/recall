#!/usr/bin/env bash
# Downloads the ONNX Runtime shared library that the embedder loads
# (internal/embed). It comes out of the onnxruntime-node package on npm, the
# version the TypeScript build this server replaced ran, so the goldens it
# wrote still hold. That one tarball has the library for every target we
# build, and its SHA-256 is pinned below.
#
# It used to come from the GitHub release archives, but
# github.com/microsoft/onnxruntime started returning 404 on 2026-11-19, which
# broke CI and the image build. The npm library is a different file from the
# release one, and both give the same vectors (docs/GO-PORT.md, M0).
#
#   scripts/fetch-onnxruntime.sh [os-arch] [dest]
#
# os-arch: darwin-arm64, linux-amd64 or linux-arm64 (default: this machine).
# dest: where libonnxruntime goes (default: lib/onnxruntime/<os-arch>).
# Prints the library path. Nothing is downloaded when it is already there.
set -euo pipefail

version="1.30.0"
# SHA-256 of https://registry.npmjs.org/onnxruntime-node/-/onnxruntime-node-$version.tgz
sha="6e3390d6b783e7be946fad629292799da28d0b42f84856e50d2c1b0383291e75"
here="$(cd "$(dirname "$0")/.." && pwd)"

target="${1:-}"
if [ -z "$target" ]; then
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "$arch" in x86_64) arch=amd64 ;; aarch64) arch=arm64 ;; esac
  target="$os-$arch"
fi

# member is the library's path in the tarball. It's saved under the release
# archive's file name, which embed.LibPath looks for.
case "$target" in
  darwin-arm64)
    member="darwin/arm64/libonnxruntime.$version.dylib"
    lib="libonnxruntime.$version.dylib"
    ;;
  linux-amd64)
    member="linux/x64/libonnxruntime.so.1"
    lib="libonnxruntime.so.$version"
    ;;
  linux-arm64)
    member="linux/arm64/libonnxruntime.so.1"
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
curl -fsSL -o "$tmp/pkg.tgz" "https://registry.npmjs.org/onnxruntime-node/-/onnxruntime-node-$version.tgz"
got="$(shasum -a 256 "$tmp/pkg.tgz" 2>/dev/null | cut -d' ' -f1 || sha256sum "$tmp/pkg.tgz" | cut -d' ' -f1)"
if [ "$got" != "$sha" ]; then
  echo "sha256 mismatch for onnxruntime-node-$version.tgz: got $got, want $sha" >&2
  exit 1
fi
tar -xzf "$tmp/pkg.tgz" -C "$tmp" "package/bin/napi-v6/$member"
mkdir -p "$dest"
cp "$tmp/package/bin/napi-v6/$member" "$dest/$lib"
echo "$dest/$lib"
