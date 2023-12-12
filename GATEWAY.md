# Cluster-IP Gateway

The purpose of this branch is to experiment with a minimal implementation of what DNS would look like for "ClusterIP `Gateway`s". For clarity, "clusterIP `Gateway`s" are `Gateway`s that are given a VIP from the ClusterCIDR of the cluster they are running in. This implementation makes few assumptions about the method by which `Gateway`s are assigned cluster IPs; those assumptions are:

1. A given `Gateway` can be designated as a "cluster-local" gateway via some mechanism (such as gatewayClass)
2. There exists a utility (likely a controller) that takes ownership of these ClusterIP gateways and assigns them one or more VIPs from one or more ClusterCIDRs of the cluster they are running in
3. The ClusterIPs assigned to a given `Gateway` are stored in the `.Status.Addresses` field of the `Gateway` resource
