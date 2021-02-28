# Holepunch

Configure UPnP routers to port-forward to Kubernetes services on your local network.

## Use Case

If you run a Kubernetes cluster behind a NAT router (e.g., on a home network) you might use a service such as [MetalLB](https://metallb.universe.tf/) to provide local-network IP addresses to your services.
But you still need to configure your router's "port forward" feature to forward traffic from the open internet (assuming you either have a static IP or dynamic DNS of some kind) to that local service IP.

Holepunch automates this last step.

## Usage

Deploy Holepunch into your server.
Then annotate services of type `LoadBalancer` with `holepunch/punch-external: "true"`.
Holepunch will then configure your router over UPnP to forward the service's ports to the declared "external IP" of the service.

## Limitations

- Only `LoadBalancer` services are supported.
- Some routers won't allow some ports (such as 80 and 443) to be configured over UPnP.
- Holepunch can't handle more than one router on your network.