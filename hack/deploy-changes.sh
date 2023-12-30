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
kubectl patch deploy coredns -p '{"spec": {"template": {"spec":{"containers":[{"name": "coredns", "imagePullPolicy":"Always"}]}}}}' -n kube-system
EXISTING_IMAGE=$(kubectl get deploy coredns -o=jsonpath='{$.spec.template.spec.containers..image}' -n kube-system)
kubectl set image deploy/coredns -n kube-system coredns="$HUB/coredns:$TAG"
if [[ "$EXISTING_IMAGE" == "$HUB/coredns:$TAG" ]]; then
  # Need to manually restart
  kubectl rollout restart deploy/coredns -n kube-system
fi

