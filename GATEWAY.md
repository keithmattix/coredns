# Cluster-IP Gateway

The purpose of this branch is to experiment with a minimal implementation of what DNS would look like for "ClusterIP `Gateway`s". For clarity, "clusterIP `Gateway`s" are `Gateway`s that are given a VIP from the ClusterCIDR of the cluster they are running in. This implementation makes few assumptions about the method by which `Gateway`s are assigned cluster IPs; those assumptions are:

1. A given `Gateway` can be designated as a "cluster-local" gateway via some mechanism (such as gatewayClass)
2. There exists a utility (likely a controller) that takes ownership of these ClusterIP gateways and assigns them one or more VIPs from one or more ClusterCIDRs of the cluster they are running in
3. The ClusterIPs assigned to a given `Gateway` are stored in the `.Status.Addresses` field of the `Gateway` resource
4. In the case of duplicates (e.g. a `Service` and `Gateway` with the same name), only the `Gateway` IP is returned

## Steps

1. Run `./hack/setup.sh` to create a kind cluster (with metalLB).
2. Build and push this local version of CoreDNS to the kind cluster with `./hack/deploy-changes.sh`.
2. Install the Gateway API CRDs with `kubectl apply -f https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.0.0/standard-install.yaml`
3. [Download istioctl](https://istio.io/latest/docs/ops/diagnostic-tools/istioctl/#install-hahahugoshortcode887s2hbhb) and run `istioctl install` to install istio.
4. Install the httpbin and sleep sample apps with `kubectl apply -f hack/samples -n default`
5. Run `nslookup` inside the sleep pod to verify that the DNS server is working as expected:

```bash
kubectl exec -it $(kubectl get pod -l app=sleep -o jsonpath={.items..metadata.name}) -c sleep -- nslookup httpbin-gateway.default.gateway.cluster.local
```

## Mirroring

One can optionally configure the Corefile to mirror Gateway addresses to the svc schema. To enable this functionality, apply the coredns-mirror configmap:

```bash
kubectl apply -f ./hack/coredns-mirror.yaml
```

You can either wait for the reload to happen automatically, or manually reload the CoreDNS pods:

```bash
kubectl rollout restart deployment coredns -n kube-system
```

Now, you can verify that the mirror is working as expected:

```bash
kubectl exec -it $(kubectl get pod -l app=sleep -o jsonpath={.items..metadata.name}) -c sleep -- nslookup httpbin-gateway.default.svc.cluster.local
```
