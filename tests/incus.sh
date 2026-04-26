#!/bin/bash
set -e
cd "$(dirname "$0")/.."
incus launch images:debian/12 casgist-test
incus exec casgist-test -- bash -c "
  apt-get update && apt-get install -y golang-go
  mkdir -p /build
"
incus file push -r . casgist-test/build/
incus exec casgist-test -- bash -c "cd /build && CGO_ENABLED=0 go test -v ./src/..."
incus delete -f casgist-test
