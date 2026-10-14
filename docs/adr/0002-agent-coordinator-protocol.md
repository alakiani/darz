---
status: accepted
---

# Agent-Coordinator protocol: HTTPS long-poll of per-Node snapshots, with commit-confirm

Agents talk to the Coordinator over plain HTTPS: they long-poll for a complete, versioned snapshot of their own desired state, apply it under a commit-confirm timer, and POST health and metering back. Nodes join in one step from the operator's laptop through a narrowly scoped API token. This builds on [ADR 0001](0001-coordinator-architecture.md), which already fixed mTLS, Agent-initiated connections, the absence of SSH credentials on the Coordinator, and offline behaviour (persisted state, no expiry). Resolved in [Agent-Coordinator protocol and Node enrollment](https://github.com/alakiani/darz/issues/11).

## Enrollment

- **One step from the laptop:** `ansible-playbook enroll.yml -i <ip>, -e name=<name> -e roles=<roles> -e tags=<tags>`. The playbook:
  1. Calls `POST /v1/nodes` with an API token, which creates the Node and returns a one-time enrollment token (valid 1 hour, bound to that Node).
  2. Installs the Agent over SSH, together with the Coordinator's CA certificate. The CA certificate is pinned, so no public CA is trusted on this channel.
  3. Starts the Agent. The Agent generates its key on the Node, sends a CSR plus the enrollment token, and receives a client certificate whose identity is the Node id. The private key never leaves the Node.
  4. Waits for the Agent's first health report before reporting success.
- **Reruns are idempotent.** A Node that never enrolled gets a fresh enrollment token. An enrolled Node only gets its Agent upgraded. Re-keying needs `-e reenroll=true`.
- **The panel offers the same operation** as a form, and shows the command with the enrollment token embedded.
- **Nobody types the public IP.** The Agent reports the address it holds, and the Coordinator records the source address it sees. A mismatch is shown.
- **The laptop's API token is scoped to `nodes:enroll`.** It can create Nodes, mint enrollment tokens, and read the result, nothing else. It is created in the panel behind a passkey or TOTP, expires after 90 days, can be revoked, and is kept in ansible-vault or the keychain. This is the pattern for every non-interactive credential, including future MCP grants. A full Admin token was rejected because a stolen laptop would then own the system.
- **Certificates last 1 year** and renew automatically over mTLS at about two thirds of their life. An Agent offline long enough for its certificate to expire is re-enrolled by rerunning the playbook.
- **Removing a Node:** deleting it in the panel makes the Coordinator refuse its certificate. An optional teardown playbook wipes the machine.

## Channel

JSON over mTLS on TCP 443, under `/v1`. `GET /v1/state?after=<version>` blocks until a newer version exists or 60 s pass. Health and metering go up as POSTs. On failure the Agent rotates through its list of Coordinator URLs.

## Configuration

- **Snapshots, not deltas.** Each version is that Node's complete desired state, so a missed version cannot corrupt anything.
- **Large, slow-changing data is referenced by content hash.** CIDR sets are an example: the Agent fetches and caches them separately.
- **A Node sees only what its Roles need.** A Bridge gets Device public keys, Rules, and candidate Paths. An Exit gets its Link peers and forwarding instructions, never the Device list, so a seized abroad Exit reveals no Users.
- **Snapshots set candidates, not live choices.** The active Path and the winning transport per Link are the Agent's runtime state. An apply never resets a live choice that is still among the candidates.

## Applying a snapshot safely (commit-confirm)

The Agent keeps the previous snapshot, applies the new one, and must complete a fresh round trip to the Coordinator within 90 s. If it cannot, it reverts and later reports `apply_failed` for that version. The Coordinator then marks the version bad for that Node and does not resend it until the state changes. Because the Agent applies only what it has just fetched, losing the Coordinator immediately afterwards implicates the change.

## Reporting

- **Health, every 30 s, from every Node:** the applied snapshot version, the Agent version, latency and loss per Link plus the transport in use, and the active Path per Exit. The Coordinator keeps the latest report and what Path computation needs. Time series belong to Prometheus.
- **Metering, from Bridges only:** bytes up and down per Device per Exit, in hourly buckets closed on disk. Uploads are keyed by `(node, device, exit, hour)` and idempotent. Up to 30 days of unacknowledged buckets are kept, so an outage keeps its hourly attribution.
- **Clocks:** every Node runs chrony, and Agents stamp hours by their own clock. Reports carry the Agent's time. Skew over 60 s raises an alert, and the data is kept but flagged.

## Upgrades

Agents are upgraded by rerunning the playbook. The panel shows Nodes running an old Agent.

## Considered options

- **gRPC bidirectional streams.** Rejected: HTTP/2-only, and harder to carry through a CDN or relay if the Coordinator ever needs a fallback path. At 4 Nodes, long-poll costs nothing that matters.
- **Delta updates.** Rejected: replay and ordering bugs would buy savings that hashed blobs already deliver.
- **Agent self-update from the Coordinator.** Rejected: it would make the Coordinator a code-execution channel to every Node, undoing ADR 0001's choice to keep SSH credentials off it.
