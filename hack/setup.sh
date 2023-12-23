#!/bin/bash
# Copied from https://github.com/istio/istio/blob/master/common/scripts/kind_provisioner.sh

set -e
set -x
WD=$(dirname "$0")
WD=$(cd "$WD"; pwd)
ROOT=$(dirname "$WD")

function kind_up() {
  kind create cluster --config "$ROOT/hack/kind-config.yaml" --wait 90s
}

function cidr_to_ips() {
    CIDR="$1"
    # cidr_to_ips returns a list of single IPs from a CIDR. We skip 1000 (since they are likely to be allocated
    # already to other services), then pick the next 100.
    python3 - <<EOF
from ipaddress import ip_network, IPv6Network;
from itertools import islice;

net = ip_network('$CIDR')
net_bits = 128 if type(net) == IPv6Network else 32;
net_len = pow(2, net_bits - net.prefixlen)
start, end = int(net_len / 4 * 3), net_len
if net_len > 2000:
  start, end = 1000, 2000

[print(str(ip) + "/" + str(ip.max_prefixlen)) for ip in islice(ip_network('$CIDR').hosts(), start, end)]
EOF
}

function install_metallb() {
  kubectl apply -f https://raw.githubusercontent.com/metallb/metallb/v0.13.7/config/manifests/metallb-native.yaml
  kubectl wait -n metallb-system pod -l app=metallb --for=condition=Ready --timeout=90s

  if [ -z "${METALLB_IPS4+x}" ]; then
    # Take IPs from the end of the docker kind network subnet to use for MetalLB IPs
    DOCKER_KIND_SUBNET="$(docker inspect kind | jq '.[0].IPAM.Config[0].Subnet' -r)"
    METALLB_IPS4=()
    while read -r ip; do
      METALLB_IPS4+=("$ip")
    done < <(cidr_to_ips "$DOCKER_KIND_SUBNET" | tail -n 100)
    METALLB_IPS6=()
    if [[ "$(docker inspect kind | jq '.[0].IPAM.Config | length' -r)" == 2 ]]; then
      # Two configs? Must be dual stack.
      DOCKER_KIND_SUBNET="$(docker inspect kind | jq '.[0].IPAM.Config[1].Subnet' -r)"
      while read -r ip; do
        METALLB_IPS6+=("$ip")
      done < <(cidr_to_ips "$DOCKER_KIND_SUBNET" | tail -n 100)
    fi
  fi

  # Give this cluster of those IPs
  RANGE="["
  for i in {0..19}; do
    RANGE+="${METALLB_IPS4[1]},"
    METALLB_IPS4=("${METALLB_IPS4[@]:1}")
    if [[ "${#METALLB_IPS6[@]}" != 0 ]]; then
      RANGE+="${METALLB_IPS6[1]},"
      METALLB_IPS6=("${METALLB_IPS6[@]:1}")
    fi
  done
  RANGE="${RANGE%?}]"

  echo '
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: default-pool
  namespace: metallb-system
spec:
  addresses: '"$RANGE"'
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: default-l2
  namespace: metallb-system
spec:
  ipAddressPools:
  - default-pool
' | kubectl apply -f -
}

kind_up
kubectl apply -f "$ROOT/hack/clusterrole.yaml"
kubectl apply -f "$ROOT/hack/coredns.yaml"
install_metallb
