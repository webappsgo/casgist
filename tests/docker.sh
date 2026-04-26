#!/bin/bash
set -e
cd "$(dirname "$0")/.."
docker run --rm -v "$(pwd)":/build -w /build golang:alpine sh -c "
  CGO_ENABLED=0 go test -v -race ./src/...
"
