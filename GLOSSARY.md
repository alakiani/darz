# DARZ

DARZ ("hole" in Farsi; tagline: there is always a way) is a censorship-circumvention VPN system: a Coordinator, a managed mesh of Nodes, and Devices that reach the open internet through an Exit chosen by policy and health.

## Language

### Infrastructure

**Bridge**:
The public-facing infrastructure node a Device connects to first. It carries traffic onward but never terminates it to the internet.
_Avoid_: Relay, entry node, ingress

**Exit**:
The infrastructure node whose public IP a Device's traffic leaves from. Exits are described by attributes such as location; a domestic Exit is an Exit, not a separate role.
_Avoid_: Egress, VPN server, endpoint

**Node**:
Any infrastructure server DARZ manages. Bridge and Exit are capabilities a Node has, not kinds of machine: one Node may be both.
_Avoid_: Server, relay, box

**Tag**:
A label on a Node, such as `country:IR`, that a Rule matches against. A Node carrying the Tags a Rule names may act as the Exit for that Rule's traffic.
_Avoid_: Label, attribute, group

**Link**:
One tunnel between two Nodes, carried by one transport (such as WireGuard, GRE, QUIC, or WebSocket) and continuously measured for health. A Link is flagged by whether it crosses the filter.
_Avoid_: Tunnel, connection, peering

**Path**:
An ordered chain of Links from a Bridge to an Exit that a Device's traffic follows. A Path may pass through more than one Bridge.
_Avoid_: Route, circuit, chain

**Coordinator**:
The central service that Users, Devices, and Nodes all coordinate with. It holds identity, topology, policy, and metering, and never touches a packet on the data path.
_Avoid_: Control plane, control node, orchestrator, server

**Agent**:
The daemon on every Node that applies what the Coordinator decides, measures Links, and reports health and usage.
_Avoid_: Daemon, node service, client

### Policy

**Rule**:
An admin-defined mapping from a set of destination CIDRs to the Tags an Exit must carry. Rules are evaluated on the Bridge, never on the Device.
_Avoid_: Policy, route, filter

### Identity

**User**:
A person with a DARZ account. A User owns one or more Devices.
_Avoid_: Account, client, member

**Device**:
One WireGuard keypair and its config, issued to exactly one User. A User with three configs has three Devices, even on one phone. Identity and usage are tracked per Device.
_Avoid_: Peer, client, profile, machine
