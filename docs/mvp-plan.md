# DARZ MVP build plan

The plan for the first build of DARZ phase one. It uses every decision on the
[wayfinder map](https://github.com/alakiani/darz/issues/1) (ADRs 0001–0008 in `docs/adr/`) and
was resolved in [MVP cut](https://github.com/alakiani/darz/issues/16). Every architectural decision
is already made; a build session that finds one still open should stop and raise it, not decide it alone.

## Acceptance test

The MVP is done when a friend on an Iranian mobile ISP (MCI or Irancell), using the stock
WireGuard app, can do all of the following on the production fleet:

1. Accept an Invite, sign in, create a Device, and import its 3 configs.
2. Browse abroad. Their public IP is an abroad Exit, and DNS resolves abroad.
3. Open a domestic site. The traffic leaves through a Node tagged `IR`, so the site sees an Iranian IP.
4. **Survive a Link or Exit failure:** we kill the Exit carrying their traffic, or block its
   crossing Link. Their traffic moves to another Exit **within seconds, with no action from them**.
   Open connections reset and new ones work.
5. **Survive a Bridge failure:** we stop their Main Bridge. **Toggling the tunnel recovers within
   about 3 minutes, and switching to a Backup config recovers within seconds** (ADR 0005; stock
   clients cannot recover hands-free).
6. See their usage on their Device page within the hour. The operator receives a Telegram alert
   for each failure in steps 4 and 5.

## Scope

**In the MVP:** everything ADRs 0001–0008 decide, except the items below. That includes:
- the Coordinator, its store, its CA, enrollment, snapshots with commit-confirm, and the nightly backup encrypted with `age`;
- Invites, password plus TOTP, and recovery Invites;
- Devices with keys generated in the browser and 3 configs each;
- metering and usage pages;
- the IR feed with its gates;
- the Bridge data plane (Rules, DNS resolved abroad, IPv6 black-holed);
- Links (plain WireGuard and AmneziaWG instances);
- Path selection (candidates, eligibility, transit failover, 64 buckets, throughput-based weights);
- Endpoint Slots with the probe Device;
- monitoring (Prometheus pull, the alert table, Telegram through an Exit, the panel banner);
- the Farsi user panel, the help page, and the admin panel as listed in the panel prototype.

**Iteration 2 (deliberately cut):**

| Cut | Why it can wait |
|---|---|
| Passkeys (WebAuthn) | Password plus TOTP already meets ADR 0001's "passkey or TOTP" for Admin actions |
| Automatic renewal of Agent certificates | Certificates last 1 year, so none expires during iteration 1 |
| Grafana dashboards other than Mesh | The admin status board covers the present; the other three are history views |

**Not in this plan:**
- Migrating the existing fleet and its Users. That is a separate effort after acceptance.
- Everything on the map's *Not yet specified* and *Out of scope* lists, including MCP, SMS, quotas, Coordinator HA, a TCP Transport, and User-chosen Exits.

## Technical baseline

- **Repository:** one Go module, `github.com/alakiani/darz`, with two binaries, `cmd/coordinator` and `cmd/agent`, and packages under `internal/`.
- **Store:** `modernc.org/sqlite` (pure Go, no cgo, static binaries), plain `database/sql` behind the store package, and SQL migrations embedded in the binary.
- **Panel:** React, Vite, and TypeScript, embedded in the Coordinator binary. An OpenAPI spec is the contract between Go and the SPA, and the TypeScript client is generated from it. A small CSS layer handles RTL; there is no component library. The user panel is in Farsi (RTL) and the admin panel in English.
- **Agent data plane:**
  - nftables rulesets are generated and applied with `nft -f`; bucket moves are element updates in one transaction.
  - `wgctrl` drives kernel WireGuard; `amneziawg-go` runs as a pinned daemon under systemd, configured over UAPI.
  - node_exporter comes from Ubuntu packages and is bound to localhost.
- **Agent:** one static binary under systemd.

## Testing

- **A lab built from network namespaces** comes first, in session 1, because every later session depends on it.
  - A Go test harness starts N Nodes as namespaces, each running the real Agent, plus the real Coordinator. The Nodes are joined by veth pairs.
  - It injects latency and loss with `tc netem`, and simulates the filter with nftables (for example "drop UDP to this port", "drop everything to this IP").
- **CI** runs the lab on GitHub's Ubuntu runner with `sudo`.
- **Each session is done** when its "proves in the lab" test is green in CI. Work test-first.
- **No staging mesh.** The first real deployment is production (session 13).

## Build sessions

Each session fits one agent session. They run in this order: the mesh first, because the hard risk is in sessions 3–5 and the lab makes them testable without any UI. By the end of session 5, a lab "Device" browses end to end through the mesh.

| # | Session | Proves in the lab | ADRs |
|---|---|---|---|
| 1 | **Skeleton and lab:** Go module layout, CI, the netns harness with netem and filter injection | The lab brings up 4 empty Nodes and tears them down | — |
| 2 | **Coordinator core and Agent protocol:** store and migrations, CA, `nodes:enroll` token, enrollment, long-poll snapshots, health POST, persisted state, commit-confirm | Agents enroll; a snapshot that cuts contact is reverted and marked bad | 0001, 0002 |
| 3 | **Links:** Transport interface, plain WireGuard and `amneziawg-go` instances, Agent-generated keys, ports and parameters relayed by the Coordinator, the probe protocol with its flags, Up, switching, retries, replacement after 24 h | Blocking one instance's UDP port moves the Link to the other instance; plain WireGuard is never tried across the filter | 0007 |
| 4 | **Bridge and Exit data plane:** shared Bridge identity, Device peers, Rules and Destination Sets in nftables, IR feed job with gates, DNS resolver and redirects, IPv6 drop, NAT on Exits | A lab Device reaches "abroad" and "domestic" addresses through the right Nodes; a hostile feed update is rejected | 0004, 0005 (identity) |
| 5 | **Path selection:** candidate derivation, eligibility flags, 64 buckets and verdict maps, failure moves, transit hand-off, rebalancing (weights from RTT and availability only) | Killing an Exit moves its buckets within seconds; killing a Bridge's direct Links hands its traffic to a neighbouring Bridge | 0008 |
| 6 | **Identity API:** Invites, password (argon2id), TOTP, sessions, recovery Invites, Users, Devices, Device cap, effective state, OpenAPI spec | API tests for every operation; a suspended User's Devices leave the Bridge snapshots | 0003 |
| 7 | **User panel:** the SPA in Farsi, key generation in the browser, 3 configs as a .zip plus a QR code each, Device page, Account, help page | A browser test issues a config, and a lab Device uses it | 0003, 0005 |
| 8 | **Metering:** Bridge counters, hourly buckets on disk, idempotent upload, rollups and retention, usage in the panel | Lab traffic appears as usage per Device per Exit; an outage loses none | 0002, 0003 |
| 9 | **Admin panel:** status board, Users, Invites, Nodes, Links (including the transport list), Rules, Destination Sets, API tokens | Browser tests | prototype |
| 10 | **Endpoint Slots:** ArvanCloud DNS client (faked in the lab), probe Device, eligibility, re-pointing | Killing a Bridge re-points its slot after 60 s | 0005 |
| 11 | **Monitoring and backups:** Agent `/metrics`, mTLS pull port, Prometheus and Alertmanager config, alert rules, Telegram relay through an Exit, panel banner, Grafana Mesh dashboard, nightly backup encrypted with `age` | The alerts from the acceptance test fire in the lab; a backup restores | 0006, 0001 |
| 12 | **Throughput test and weights:** the capped test, passive peaks, weight formula, cutoff | Bucket shares follow injected capacity differences | 0008 |
| 13 | **Deployment and acceptance:** `coordinator.yml` and `enroll.yml` playbooks, deploying to the new fleet, the acceptance test | The acceptance test above | 0002 |

## Deployment

- **Binaries are pushed, never pulled.**
  - CI builds release artifacts: the Agent, the Coordinator, the pinned `amneziawg-go`, and the pinned Prometheus, Alertmanager, and Grafana.
  - The operator downloads them to the laptop once per release, and the Ansible playbooks push them over SSH.
  - Only Ubuntu's own packages (chrony, nftables, node_exporter) come from a Node's apt mirror.
  - So installing or rebuilding a domestic Node never depends on reaching abroad.
- **Playbooks:** `coordinator.yml` installs the Coordinator VM. `enroll.yml` enrolls a Node (ADR 0002), and rerunning it upgrades the Agent.

### Before session 13: what to provision

- [ ] Coordinator VM, domestic, ideally at a different provider from the Bridges (ADR 0001).
- [ ] 2 domestic Nodes in datacenters on different ASes, whose IPs can reach abroad (check before committing; the research found source-IP allowlisting).
- [ ] 2 abroad Nodes at different providers.
- [ ] Ubuntu 26.04 on all of them.
- [ ] ArvanCloud DNS zone for `4miz.ir`, with an API key able to edit `a`, `b`, and `c.4miz.ir`.
- [ ] Telegram bot token and alert channel.
- [ ] Object storage abroad for the nightly backup, plus the operator's `age` key on the laptop only.
- [ ] Public TLS certificate for `4miz.ir`.

This new fleet becomes production. The old fleet keeps serving Users until the separate migration effort.
