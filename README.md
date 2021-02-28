# Holepunch

Configure UPnP routers to port-forward to Kubernetes services on your local network.

## Use Case

If you run a Kubernetes cluster behind a NAT router (e.g., on a home network) you might use a service such as [MetalLB](https://metallb.universe.tf/) to provide local-network IP addresses to your services.
But you still need to configure your router's "port forward" feature to forward traffic from the open internet (assuming you either have a static IP or dynamic DNS of some kind) to that local service IP.

This typically requires you to cordinate IP addresses, set `spec.loadBalancerIP` and hope that no other service used it first, and then configure your router manually.
Holepunch automates this process, and configures your router using UPnP to whatever the local network IP is.

## Usage

Deploy Holepunch into your cluster.
A container image is available at `ghcr.io/jameslaverack/holepunch:v0.1.0`.
You can use the provided Makefile to produce the YAML and deploy to your current kube config using:

```bash
export IMG='ghcr.io/jameslaverack/holepunch:v0.1.0'
make deploy
```

Once Holepunch is deployed, annotate services of type `LoadBalancer` with `holepunch/punch-external: "true"`.
Holepunch will then configure your router over UPnP to forward the service's ports to the declared "external IP" of the service.

## Limitations

- Only `LoadBalancer` services are supported.
- Some routers won't allow some ports (such as 80 and 443) to be configured over UPnP.
- Holepunch can't handle more than one router on your network.
- To work inside your Kubernetes cluster, the holepunch Pod must bind to the host network and expose some UDP ports.
  This means that no more than one holepunch pod can run at once, and no other UPnP services can work at the same time on the same cluster.
  
