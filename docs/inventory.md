# Infrastructure inventory

Recorded 2026-10-14 while resolving the wayfinder ticket
[Inventory the current infrastructure](https://github.com/alakiani/darz/issues/2).
Deliberately thin: DARZ discovers what works, so this file records only what
no probe can learn.

## Nodes

| Fact | Value |
| --- | --- |
| Domestic Nodes (inside Iran) | 2 |
| Nodes abroad | 2 |
| OS today | Mix of Ubuntu and Mikrotik CHR |
| OS target | Ubuntu 26.04 on every Node; CHR Nodes are migrated, not managed |
| Providers | Varied (AWS, GCore, Lightnode, Sotoon, ArvanCloud, others) and treated as interchangeable |
| What a Node is assumed to have | Ubuntu, SSH access, one public IPv4, inbound UDP and TCP |
| What a Node is never assumed to have | Floating or anycast IPs, BGP, shared L2 with another Node, provider APIs |

Nodes carry **Roles** (Bridge, Exit; a Node may hold both) and **Tags**
(for example `IR` for domestic, a provider name, anything a Rule needs to
match). Roles say what a Node does; Tags say what it is.

## Domains

| Domain | Intended use | Blocked inside Iran (2026-10-14) |
| --- | --- | --- |
| `4miz.ir` | Main service panel | No |
| `id.4miz.ir` | Identity provider | No |
| `chaarmiz.ir` | Secondary, reserved | No |

Both are `.ir` domains, registered through IRNIC and therefore under
Iranian jurisdiction. That is a fact for the Coordinator hosting decision,
not a problem solved here.

## Scale

| Fact | Value |
| --- | --- |
| Users | about 50 |
| Devices | about 100 |
| Monthly traffic | not recorded |

## Constraints adopted with this inventory

1. **Transport viability is discovered, not configured.** The Agent on every
   Node tries every transport in an admin-confirmed candidate list on every
   Link, scores each by health, keeps working ones as warm standbys, and
   re-probes on a schedule. Nobody records which tunnel works from which
   datacenter.
2. **Minimum provider dependency.** A design may use a provider feature when
   present but never require one. The lowest common denominator in the table
   above is the contract.
3. **Nodes join by SSH.** The operator adds a Node by handing the system SSH
   access; provisioning installs the Agent and enrolls the Node. The mechanism
   (Coordinator-driven or a runner pipeline) is decided in
   [Agent-Coordinator protocol and Node enrollment](https://github.com/alakiani/darz/issues/11).

## Deliberately not recorded

Per-datacenter transport results, provider bandwidth caps and port policies,
and per-ISP reachability from Devices. All of these change with the filter
and are the system's job to learn.
