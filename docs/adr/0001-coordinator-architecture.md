---
status: accepted
---

# Coordinator: one domestic Go binary on SQLite, holding as little as possible

The Coordinator is a single Go binary with its web assets embedded and SQLite as its only store, on a dedicated small VM inside Iran. Most Users and half the Nodes are domestic, and a domestic Coordinator stays reachable for them during an international shutdown. Because that puts the membership list of a circumvention service under Iranian jurisdiction, the Coordinator is built to hold as little as possible and to give nothing away if it is seized. Resolved in [Coordinator architecture](https://github.com/alakiani/ways/issues/7).

## Decisions

**Shape.** One binary and one SQLite file (WAL mode), with every query going through a single store package. Background jobs run as goroutines in that binary: the daily RIPE CIDR refresh with its validation gates, Path computation, metering rollups, and DNS updates for the Bridge hostname. Prometheus and Grafana run separately. Anything not core to phase one, accounting included, is left out.

**Hosting.** A dedicated domestic VM, ideally at a different provider from the Bridges. It is not a Node, and its IP never carries a Link. Panel, API, and Agent endpoints live on `4miz.ir`; `id.4miz.ir` is dropped.

**Data minimisation** (this is the condition for domestic hosting):
- User: a display handle and credentials only. No email, phone, or real name. An Admin delivers Invites out of band, for example on Telegram.
- Device: public key, name, and owning User. Private keys are never stored.
- Metering: bytes per Device per Exit per hour, kept for 90 days and then folded into monthly totals. Never destinations, DNS, or flow logs. A future accounting system is fed by exporting these rollups. *(Retention superseded by [ADR 0003](0003-identity-devices-metering.md): hourly for 7 days, daily for 90 days, then monthly.)*
- Device source IPs are never persisted. They exist only in a Bridge's live WireGuard state.

**Backups.** A nightly SQLite `.backup`, encrypted with `age` to a key held only on the operator's laptop and pushed to object storage abroad. A seized host cannot read its own backups. Losing up to a day of metering is accepted.

**Callers and auth.**
- *Users* sign in through built-in auth. An Invite leads to registering a passkey when WebAuthn works on the device, and otherwise to setting a password (argon2id). Admin actions require a passkey or TOTP. Pocket ID was rejected because it is passkey-only, which fails on inconsistent Android WebAuthn. Authentik was rejected as too heavy.
- *Agents* authenticate with mTLS. The Coordinator runs a small CA and issues each Node a client certificate at enrollment. The Agent API is on its own listener, and the user-facing listener never accepts client certificates.
- *Direction*: Agents always dial the Coordinator, and the Coordinator never dials a Node. The Coordinator holds no SSH credentials. Provisioning is an Ansible playbook run from the operator's laptop, which carries a one-time enrollment token minted by the Coordinator. The enrollment protocol is decided in [Agent-Coordinator protocol and Node enrollment](https://github.com/alakiani/ways/issues/11). *(Narrowed by [ADR 0006](0006-monitoring-and-alerting.md): Prometheus may pull each Agent's `/metrics`, and the Coordinator may hand alerts to an Exit for relay. State, commands, and enrollment still flow only from Agent to Coordinator.)*

**API style.** The panel is a SPA, embedded in the binary, over a JSON API. Every Admin capability is a JSON API operation first, and the panel and any future tool are clients of that API. This keeps an MCP server (for example, changing topology from a voice assistant) a thin adapter: it needs OAuth 2.1 and a propose → confirm step on writes, and it is not in the MVP unless [MVP cut](https://github.com/alakiani/ways/issues/16) pulls it forward. The MCP endpoint must be reachable from abroad.

**When the Coordinator is unreachable.** Each Agent persists its last applied desired state to disk with a version number: Devices and their public keys, Rules and their CIDR sets, ranked candidate Paths *(replaced by scoring parameters in [ADR 0008](0008-path-selection-and-exit-balancing.md))*, the candidate transport list, and Link peers. It re-applies that state on boot, with no expiry. A Bridge keeps accepting every Device it knows until a revocation arrives, and an Exit keeps forwarding until it receives a new command. A Device added or revoked during an outage takes effect only when contact returns, which is accepted. Agents keep cumulative per-Device counters on disk and upload deltas on reconnect, so an outage loses no metering. Each Agent exposes `coordinator_last_contact_seconds`, and the Coordinator alerts on Agents it has not heard from.

**What must be true now so HA is not a rewrite** (HA itself is deferred):
1. All authoritative state lives in the store; nothing is authoritative only in memory. Jobs are safe to run twice or behind a leader lock.
2. Desired state is versioned and idempotent, so an Agent can take it from any Coordinator instance.
3. Agents carry a list of Coordinator URLs from day one, even though it has one entry now.
4. The CA key and session secret live outside the database file, so instances can share them.

Deferred to HA: running several instances, Postgres or LiteFS replacing SQLite behind the store package, leader election, and failover.

## Considered options

- **Hosting abroad**, alone or behind a thin domestic TCP front. It keeps the data out of Iranian jurisdiction, but it loses the Coordinator during a shutdown and makes domestic Agents depend on cross-border paths. Rejected in favour of domestic hosting with data minimisation.
- **Postgres**, rejected for weight at this scale (~50 Users, ~100 Devices, 4 Nodes). The store package is the seam for revisiting it.
- **Litestream**, rejected for nightly encrypted snapshots: one less daemon, and nothing unencrypted ever leaves the host.
- **Server-rendered panel (templates + htmx)**, rejected because an API-first design is what makes the MCP adapter cheap.
