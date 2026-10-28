#!/usr/bin/env bash
# Build portable archives containing the interpreter, server and MIT license.
set -euo pipefail
version=$1
commit=$2
out=$(realpath -m "${3:-dist}")
mkdir -p "$out"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64; do
  os=${target%/*}
  arch=${target#*/}
  name="brote_${version}_${os}_${arch}"
  stage=$(mktemp -d)
  mkdir "$stage/$name"
  ext=""
  if [ "$os" = windows ]; then ext=.exe; fi
  flags="-s -w -X github.com/fclaude/brote/version.Version=$version -X github.com/fclaude/brote/version.PackageRevision=v$version -X github.com/fclaude/brote/version.SourceCommit=$commit"
  for command in brote brote-lsp; do
    if [ -d "cmd/$command" ]; then
      CGO_ENABLED=0 GOOS=$os GOARCH=$arch go build -trimpath -buildvcs=false -ldflags "$flags" -o "$stage/$name/$command$ext" "./cmd/$command"
    fi
  done
  cp LICENSE "$stage/$name/"
  if [ "$os" = windows ]; then
    (cd "$stage" && zip -qr "$out/$name.zip" "$name")
  else
    tar -czf "$out/$name.tar.gz" -C "$stage" "$name"
  fi
  rm -rf "$stage"
done
(cd "$out" && sha256sum brote_* > SHA256SUMS)
