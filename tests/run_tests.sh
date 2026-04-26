#!/bin/bash
# Auto-detect Incus or Docker and run tests
set -e
if command -v incus >/dev/null 2>&1; then
    echo "Running tests with Incus..."
    exec ./tests/incus.sh
elif command -v docker >/dev/null 2>&1; then
    echo "Running tests with Docker..."
    exec ./tests/docker.sh
else
    echo "ERROR: Neither Incus nor Docker found"
    exit 1
fi
