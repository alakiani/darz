# WAYS

WAYS ("hole" in Farsi; tagline: there is always a way) is a censorship-circumvention VPN system: a Coordinator, a managed mesh of Nodes, and Devices that reach the open internet through an Exit chosen by policy and health.

## Language

### Infrastructure

**Bridge**:
The public-facing infrastructure node a Device connects to first. It carries traffic onward but never terminates it to the internet.
_Avoid_: Relay, entry node, ingress

**Exit**:
The infrastructure node whose public IP a Device's traffic leaves from. Exits are described by attributes such as location; a domestic Exit is an Exit, not a separate role.
_Avoid_: Egress, VPN server, endpoint

**Node**:
Any infrastructure server WAYS manages: an Ubuntu machine with SSH access and one public IPv4. A Node holds one or more Roles and any number of Tags.
_Avoid_: Server, relay, box, VM

**Role**:
What a Node does: Bridge or Exit. Roles are capabilities, not kinds of machine, so one Node may hold both.
_Avoid_: Type, kind, mode

**Tag**:
A label on a Node, such as `IR`, that a Rule matches against. A Tag names the traffic a Node may carry as an Exit: a Node tagged `IR` may be the Exit for traffic to Iran. The `IR` Tag also marks a Node as inside the filter.
_Avoid_: Label, attribute, group

**Link**:
One tunnel between two Nodes, carried at any moment by one Transport chosen from its candidates by health, and continuously measured. A Link crosses the filter when exactly one of its Nodes is tagged `IR`.
_Avoid_: Tunnel, connection, peering

**Transport**:
A way of carrying a Link's traffic between two Nodes, such as GRE, plain WireGuard or AmneziaWG. Which Transports work on a Link is discovered by trying them, never configured.
_Avoid_: Protocol, carrier, tunnel type

**Path**:
An ordered chain of Links from a Bridge to an Exit that a Device's traffic follows. A Path may pass through more than one Bridge.
_Avoid_: Route, circuit, chain

**Endpoint Slot**:
A stable hostname carried in Devices' configs that the Coordinator points at a healthy Bridge. Every Device has one config per Endpoint Slot.
_Avoid_: Server, entry, gateway, endpoint

**Coordinator**:
The central service that Users, Devices, and Nodes all coordinate with. It holds identity, topology, policy, and metering, and never touches a packet on the data path.
_Avoid_: Control plane, control node, orchestrator, server

**Agent**:
The daemon on every Node that applies what the Coordinator decides, measures Links, and reports health and usage.
_Avoid_: Daemon, node service, client

### Policy

**Destination Set**:
A named set of destination CIDRs, such as the Iranian address space, that a Rule routes as one. It may be fed automatically and adjusted by an Admin.
_Avoid_: Zone, prefix list, geo list, CIDR list

**Rule**:
An admin-defined mapping from a Destination Set to the Tags an Exit must carry. Rules are evaluated on the Bridge, never on the Device.
_Avoid_: Policy, route, filter

### Identity

**User**:
A person with a WAYS account. A User owns one or more Devices.
_Avoid_: Account, client, member

**Device**:
One WireGuard keypair and the configs that carry it, one per Endpoint Slot, issued to exactly one User. A User with three keypairs has three Devices, even on one phone. Usage is recorded per Device and always belongs to its User, even after the Device is revoked.
_Avoid_: Peer, client, profile, machine

**Admin**:
A User permitted to manage Users, Invites, Nodes, Links, and Rules. Being an Admin is a permission on a User, not a Role.
_Avoid_: Role, superuser, operator

**Invite**:
A single-use link through which a person becomes a User.
_Avoid_: Signup link, registration token, voucher

**Suspended**:
The state of a User whose Devices are cut off but not revoked, so resuming the User restores them without new configs.
_Avoid_: Disabled, banned, frozen

**Usage**:
The bytes a Device moved, up and down, as the Coordinator records them. A User's usage is the sum over all their Devices, revoked ones included; only deleting the User erases it.
_Avoid_: Traffic, bandwidth, consumption

**Quota**:
The bytes, up and down together, a User may move in one Quota Period, shared across all their Devices. A Quota is either a number or unlimited. A User who has used up their Quota is blocked until the next Quota Period, unless a Top-up or Reset raises what they may still use.
_Avoid_: Limit, cap, plan, allowance

**Quota Period**:
The recurring window a Quota is counted over, one month long, starting on the User's Renewal Day. Usage counts from zero when a new one starts. Every User has Quota Periods, even with an unlimited Quota.
_Avoid_: Billing cycle, month

**Renewal Day**:
The day of the month a User's Quota Periods start, taken from the day the User registered. In a month too short for it, the period starts on that month's last day. Changing a User's Quota never moves it.
_Avoid_: Anchor, billing date, reset day

**Top-up**:
A fixed number of bytes an Admin adds to what a User may use in the current Quota Period only. It never changes recorded Usage.
_Avoid_: Bonus, extra data, credit

**Reset**:
An addition to the current Quota Period equal to what the User has used so far, bringing them back to a full Quota. Recorded Usage stays as it was; only what the User may still use changes.
_Avoid_: Clear, zero, wipe

**Refill**:
A Reset an Admin gives a User to hold and redeem themselves, until it expires. A User may hold several, and they carry across Quota Periods until redeemed or expired.
_Avoid_: Voucher, token, coupon, reset button
