# Cluster-IP Gateway

The purpose of this branch is to experiment with a minimal implementation of what DNS would look like for "ClusterIP `Gateway`s". For clarity, "clusterIP `Gateway`s" are `Gateway`s that are given a VIP from the ClusterCIDR of the cluster they are running in. This implementation makes few assumptions about the method by which `Gateway`s are assigned cluster IPs; those assumptions are:

1. A given `Gateway` can be designated as a "cluster-local" gateway via some mechanism (such as gatewayClass)
2. There exists a utility (likely a controller) that takes ownership of these ClusterIP gateways and assigns them one or more VIPs from one or more ClusterCIDRs of the cluster they are running in
3. The ClusterIPs assigned to a given `Gateway` are stored in the `.Status.Addresses` field of the `Gateway` resource

## Steps

1. Run `./hack/setup.sh` to create a kind cluster (with metalLB).
2. Build and push this local version of CoreDNS to the kind cluster with `./hack/deploy-changes.sh`.
2. Install the Gateway API CRDs with `kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.0.0/standard-install.yaml`
3. [Download istioctl](https://istio.io/latest/docs/ops/diagnostic-tools/istioctl/#install-hahahugoshortcode887s2hbhb) and run `istioctl install` to install istio.
4. Install the httpbin and sleep sample apps with `kubectl apply -f hack/samples`
5. Run `nslookup` inside the sleep pod to verify that the DNS server is working as expected:

```bash
kubectl exec -it $(kubectl get pod -l app=sleep -o jsonpath={.items..metadata.name}) -c sleep -- nslookup httpbin-gateway.default.svc.cluster.local
```
