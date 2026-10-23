# Deploying the fleet

Binaries are pushed from the laptop, never pulled by a Node (docs/mvp-plan.md, Deployment).

1. **Provision:** `scripts/provision-fleet.sh` walks through renting the VMs and collecting the
   credentials. It writes `~/.ways/fleet.env` and `~/.ways/inventory.yml`.
2. **Fetch a release:** `scripts/fetch-release.sh v0.1.0` downloads it to `~/.ways/releases/v0.1.0`
   and checks its SHA256SUMS. In Iran, use a VPN: Grafana alone is ~450 MB.
3. **Coordinator:** `scripts/ways-ansible coordinator.yml`. The first run brings back
   `~/.ways/ca.crt` (the CA certificate Agents pin) and `~/.ways/enroll-token` (the laptop's
   `nodes:enroll` token, ADR 0002), and prints the first Admin's Invite.
4. **Nodes:** `scripts/ways-ansible enroll.yml` enrolls every Node in `domestic` (Bridge and Exit,
   tagged `IR`) and `abroad` (Exit), and waits for each one's first health report.
   `-l ir-1` limits it to one Node; `-e reenroll=true` re-keys it.

Rerunning either playbook with a newer release upgrades in place; with the same release it
changes nothing. Both first check every file of the release against its `SHA256SUMS` and stop,
naming the bad files, if a download was cut off or damaged. `WAYS_RELEASE=v0.1.0` picks a release; the default is the newest one fetched.

Grafana and Prometheus listen on the Coordinator's loopback only. Admins open Grafana through the
panel at https://wayfinder.ir/grafana/ (the "Grafana" link in the admin nav), signed in with TOTP.
Prometheus needs a tunnel: `ssh -L 9090:127.0.0.1:9090 ubuntu@<coordinator>`, then open
http://127.0.0.1:9090.

A release is published by pushing a tag (`git tag v0.1.0 && git push origin v0.1.0`). CI's
`deploy` job runs both playbooks against Ubuntu 26.04 containers on every PR
(`internal/deploytest`).
