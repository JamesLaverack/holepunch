# Holepunch

Configure UPnP routers to port-forward to Kubernetes services on your local network.

## Use Case

If you run a Kubernetes cluster behind a NAT router (e.g., on a home network) you might use a service such as [MetalLB](https://metallb.universe.tf/) to provide local-network IP addresses to your services.
But you still need to configure your router's "port forward" feature to forward traffic from the open internet (assuming you either have a static IP or dynamic DNS of some kind) to that local service IP.

This typically requires you to cordinate IP addresses, set `spec.loadBalancerIP` and hope that no other service used it first, and then configure your router manually.
Holepunch automates this process, and configures your router using UPnP to whatever the local network IP is.

## Usage

Deploy Holepunch into your cluster.
A container image is available at `ghcr.io/jameslaverack/holepunch`.
You can use the provided Makefile to produce the YAML and deploy to your current kube config.

For example, to deploy version `v0.1.0`:
```bash
export IMG='ghcr.io/jameslaverack/holepunch:v0.1.0'
make deploy
```

Once Holepunch is deployed, annotate services of type `LoadBalancer` with `holepunch/punch-external: "true"`.
Holepunch will then configure your router over UPnP to forward the service's ports to the declared "external IP" of the service.

### Using Different External Ports

If you want to expose a different port on your router than the Kubernetes service port, you can map this with an annotation.
Holepunch looks for annotations with the prefix `holepunch.port/`, followed by the service's port number.
The value of this annotation is the desired external port.
Note that annotations must have string YAML values, so the external port number must be templated as a string.

For example, if a service exposes port 80, the annotation `holepunch.port/80: "3000"` could be used.
This would cause Holepunch to make a UPnP mapping from an external port 3000 to port 80 on the local network.

## Limitations

- Only `LoadBalancer` services are supported.
- Some routers won't allow some ports (such as 80 and 443) to be configured over UPnP.
- Holepunch can't handle more than one router on your network.
- To work inside your Kubernetes cluster, the holepunch Pod must bind to the host network and expose some UDP ports.
  This means that no more than one holepunch pod can run at once, and no other UPnP services can work at the same time on the same cluster.
  
