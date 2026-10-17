# Deploying the fleet

Binaries are pushed from the laptop, never pulled by a Node (docs/mvp-plan.md, Deployment).

1. **Provision:** `scripts/provision-fleet.sh` walks through renting the VMs and collecting the
   credentials. It writes `~/.darz/fleet.env` and `~/.darz/inventory.yml`.
2. **Fetch a release:** `scripts/fetch-release.sh v0.1.0` downloads it to `~/.darz/releases/v0.1.0`
   and checks its SHA256SUMS. In Iran, use a VPN: Grafana alone is ~450 MB.
3. **Coordinator:** `scripts/darz-ansible coordinator.yml`. The first run brings back
   `~/.darz/ca.crt` (the CA certificate Agents pin) and `~/.darz/enroll-token` (the laptop's
   `nodes:enroll` token, ADR 0002), and prints the first Admin's Invite.
4. **Nodes:** `scripts/darz-ansible enroll.yml` enrolls every Node in `domestic` (Bridge and Exit,
   tagged `IR`) and `abroad` (Exit), and waits for each one's first health report.
   `-l ir-1` limits it to one Node; `-e reenroll=true` re-keys it.

Rerunning either playbook with a newer release upgrades in place; with the same release it
changes nothing. `DARZ_RELEASE=v0.1.0` picks a release; the default is the newest one fetched.

Grafana and Prometheus listen on the Coordinator's loopback only:
`ssh -L 3000:127.0.0.1:3000 root@<coordinator>`, then open http://127.0.0.1:3000.

A release is published by pushing a tag (`git tag v0.1.0 && git push origin v0.1.0`). CI's
`deploy` job runs both playbooks against Ubuntu 26.04 containers on every PR
(`internal/deploytest`).
