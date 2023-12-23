#!/bin/bash

set -e
set -x

WD=$(dirname "$0")
WD=$(cd "$WD"; pwd)
ROOT=$(dirname "$WD")
TAG="${TAG:-latest}"
HUB="${HUB:-localhost:5000}"
OS="${OS:-linux}"
ARCH="${ARCH:-amd64}"

make -f "$ROOT/Makefile.release" release
docker build -f Dockerfile "$ROOT/build/$OS/$ARCH" -t "$HUB/coredns:$TAG"
docker push "$HUB/coredns:$TAG"
kubectl set image deploy/coredns -n kube-system coredns="$HUB/coredns:$TAG"
