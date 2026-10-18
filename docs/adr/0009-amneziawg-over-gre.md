---
status: proposed
---

# AmneziaWG over GRE is the first candidate on Links that cross the filter

On Links that cross the filter, the first candidate Transport becomes AmneziaWG carried inside GRE (IP protocol 47) instead of over UDP. AmneziaWG over UDP stays on every such Link as warm standby. This amends [ADR 0007](0007-link-transport-discovery.md), which rejected GRE. The reason is what the first production deployment measured on 2026-10-17 and 2026-10-18:

| From a Sotoon Bridge (AS204533) to | AmneziaWG over UDP | Small or random UDP packets | GRE |
| --- | --- | --- | --- |
| AWS London (AS16509) | Up | arrive | **Up both ways, 79 ms, 107–173 Mbit/s for 70 s, 0% loss** |
| GCore (AS199524), Hetzner (AS24940), OVH (AS16276), Vultr (AS20473) | **dropped**: the tunnel's packets leave the Bridge and never arrive | arrive, even on the tunnel's own ports | not yet measured |

So the filter is not dropping UDP as such. It recognises tunnel flows to ordinary hosting networks and drops them, and it did the same to a TCP flow: the Coordinator's mTLS scrape of GCore connected, then timed out. GRE is a different protocol, outside the UDP flows the filter evidently classifies. ADR 0007's objections still hold, and the design answers each one: there is no encryption, so AmneziaWG runs inside; plain WireGuard brings no benefit, so plain WireGuard is not what runs inside; and GRE fails protocol allowlists, so AmneziaWG over UDP stays as the fallback.

**Accepted only when** a GRE flow from a domestic Bridge to a non-AWS Exit survives the same test, a sustained bulk flow for at least 60 s. AWS is also reachable with AmneziaWG over UDP, so the AWS result alone cannot show that GRE gets past the block.

## The Transport

- **AmneziaWG over GRE** is a new Transport in the candidate list (ADR 0007). The Agent brings up a GRE interface between the two Nodes, and an `amneziawg-go` instance whose UDP runs over that interface's inner /31. The outer packet is IP protocol 47; inside it is an AmneziaWG datagram with the instance's own junk packets, padding and decoys.
- **Not plain WireGuard inside.** GRE does not hide what it carries, so plain WireGuard's fixed handshake would show in every packet, and ADR 0007 keeps it off Links that cross the filter for that reason.
- **Like every Transport, it is discovered, not configured.** It is first in the candidate list and Admins can disable it, like any Transport. Whether it works on a given Link is still decided by probing.

## One GRE carrier per Link

- **GRE has no ports.** A GRE tunnel is identified by the pair of public addresses, and NAT gateways track at most one GRE flow per address pair. A Link therefore has at most one GRE carrier.
- **Replacement after 24 h (ADR 0007) changes only the AmneziaWG instance inside**: its inner port and parameters. The carrier itself has no port to change, so the mesh cannot rediscover a GRE fingerprint the way it does for UDP instances.
- **Candidates on a Link that crosses the filter:** 1 AmneziaWG-over-GRE instance, then the existing 2 AmneziaWG-over-UDP instances. **Same-side Links are unchanged** (plain WireGuard, then AmneziaWG), because nothing filters them.

## Addresses: both ends need the other's public address

- GRE does not roam, so unlike WireGuard both ends need the peer's public address. Each end uses its own interface address as the local end, and the peer's public address as the remote end. That holds behind NAT too: in the test, Sotoon's `10.10.10.215` and AWS's `172.31.7.75` both worked unchanged.
- **Which public address:** the one the Coordinator sees the Node's Agent connect from (the Node's seen address, ADR 0002), sent in both ends' snapshots. Nobody types an IP.
- **Accepted gap:** a Node whose outbound NAT address differs from the address that reaches it from outside gets no working GRE carrier. Sotoon already NATs one of our VMs that way: the Coordinator VM reaches the internet from `31.171.101.178` and is reached at `78.109.207.63`. A Bridge set up like that would hit this gap. Probing shows the GRE instance as never Up, and the Link carries traffic on AmneziaWG over UDP. Nothing is configured per Node to work around it.

## Who initiates, keepalives, MTU

- **The `IR` end sends first**, as ADR 0007 requires, so the NAT in front of the Bridge opens the GRE mapping. The once-a-second in-tunnel probes keep it open, so no extra keepalive is needed.
- **The inner MTU drops by GRE's 24 bytes.** The Agent sets the AmneziaWG interface's MTU from the outer MTU; Device and Link MTUs do not otherwise change.

## Security

- **The outer GRE layer is unauthenticated**, so anyone can send GRE to an Exit from a spoofed Bridge address. Such packets reach only the AmneziaWG instance, which drops anything not from its peer's key. A GRE interface exists only for a known peer's address, and the Agent's nftables accept protocol 47 only from the addresses of current GRE peers.
- **Provisioning must let IP protocol 47 through** at the provider (for example a security-group rule for protocol 47, or "all traffic", from the Bridges). The wizard's reachability step checks it.

## Consequences

- **The filter now sees GRE between a Bridge and its Exits.** If it starts blocking GRE, or blocking an Exit's address for carrying it, the Link switches to AmneziaWG over UDP within about a second. Probing alone does it: no Admin acts, and that's the warm standby ADR 0007 already pays for.
- **Path selection is unchanged.** It still sees one health per Link, from the active instance.
- **The lab must be able to block GRE separately from UDP**, so the test "GRE blocked: the Link moves to AmneziaWG over UDP" can run.

## Considered options

- **Plain WireGuard over GRE:** simpler, kernel only, but it puts WireGuard's fixed handshake in clear inside every GRE packet.
- **GRE with IPsec (ESP):** a second protocol (50) to get past the filter, a second key system beside the Link keypair, and ESP is commonly blocked.
- **Replacing AmneziaWG over UDP with GRE entirely:** it would leave no fallback if GRE is blocked, and GRE is the first thing an allowlist drops.
- **A TCP Transport (VLESS+REALITY), ADR 0007's planned answer:** still open. The GCore result suggests the filter also drops TCP flows to hosting networks, so it is not obviously better, and it costs the second process model ADR 0007 deferred.
