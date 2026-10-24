---
status: accepted
---

# Usage belongs to the User, and a per-User Quota is counted from their Renewal Day

Usage is summed per User, revoked Devices included. A User may have a **Quota**: either a number of bytes or unlimited, counted up and down together and shared across all their Devices. It is counted over a monthly **Quota Period** that starts on the User's **Renewal Day**. Running out blocks the User's Devices, or slows them if an Admin chose that. Admins can raise what the User may still use, and Users can redeem **Refills** they hold. This amends [ADR 0003](0003-identity-devices-metering.md): it fills its quota hooks, adds a per-User period total to its retention table, and puts `throttled(rate)` into service. It was resolved on the [per-User Usage and Quota map](https://github.com/alakiani/ways/issues/68).

## Usage per User

- **A Device's Usage always belongs to its User.** Revoking a Device keeps its Usage (ADR 0003 already stores it). It now counts toward the User's totals, and the User sees it as one **Revoked Devices** row. Only deleting the User erases Usage.
- **Both directions count:** up plus down, the same bytes metering already records.

## Quota Periods

- **Renewal Day:** the day the User registered (redeemed their Invite). Users who exist before this change take their `created_at`. Every User has Quota Periods, unlimited ones included, so history looks the same for everyone. Changing a Quota never moves the Renewal Day, and no remedy moves it either.
- **A period starts at 00:00 UTC** on the Renewal Day (03:30 Tehran, which has no DST). That lines up with the UTC daily rows, so the current period's total is always exact: a period is at most 31 days, and hourly (7 days) plus daily (90 days) rows cover it.
- **Short months:** in a month without the Renewal Day, the period starts on that month's last day, and it goes back to the Renewal Day when the month allows. It never drifts.
- **Closed periods:** when a period ends, the Coordinator stores **one total per User per period**. This is about monthly resolution, so it stays within ADR 0003's no-fine-timing rule, and it is erased with the User.
  - Per-Device monthly rows stay on UTC calendar months, so they don't sum to period totals.
  - Views never mix the two: period history shows totals, and the per-Device breakdown is offered only for periods within the last ~90 days.

| Resolution (added to ADR 0003's table) | Kept for |
| --- | --- |
| Per User per Quota Period, total | until the User is deleted |

## Quota and running out

- **A Quota is a number of bytes or unlimited.**
  - **Defaults:** unlimited for existing Users and on the Invite form. Admins are unlimited unless they set their own.
  - **Units:** Quotas are entered and shown in decimal GB (1 GB = 10⁹ bytes).
- **What may still be used** in a period is the Quota, plus that period's Top-ups and Resets, minus the period's Usage. When that reaches zero, the User has run out.
- **Each User has a setting for what happens then:**
  - **Block** (the default): the effective state is `blocked`.
  - **Slow to N Mbit/s**: the effective state is `throttled(rate)`.

  The Admin sets this on the User and may preset it on an Invite. Throttled traffic still counts as Usage.
- **Precedence of reasons:** `device revoked` > `user suspended` > `quota used up`. A resumed User who is still out of Quota stays blocked.
- **The reason shows in the panel only.** Snapshots keep leaving blocked Devices out, as today, and carry a rate only for throttled ones.

## Remedies

Remedies never change recorded Usage; each one adds to what the User may still use in the current period.

- **Top-up:** an Admin adds a number of bytes to the current period only. Unused bytes don't carry over.
- **Reset:** adds exactly what the User has used so far in the period, bringing them back to a full Quota.
- **Refill:** a Reset an Admin gives a User to hold.
  - **Expiry:** each Refill has its own, set when it's given (default 30 days).
  - **Holding:** a User may hold several, and unused ones carry across periods until redeemed or expired.
  - **Redeeming:** allowed at any time, after a confirmation showing what it adds now. Users with an unlimited Quota can't redeem one.
- **Admin actions:** give Refills, **Reset now** (a Refill given and redeemed at once), Top-up, and edit the Quota. An Invite may preset the Quota, the running-out setting, and a number of Refills with their expiry.
- **Every remedy, and every Quota edit, takes effect on the next snapshot,** right away.
- **Warnings are panel only:** a banner at 80% and at 100%. At 100% it shows the next Renewal Day, and a Redeem button if the User holds a Refill. A blocked User can still sign in to the panel.

## Enforcement and its timing

- **The Quota is checked when usage arrives:** the health handler re-evaluates the User right after `PutUsage` and reconciles if their state changed.
- **Reports get faster.** The Agent:
  - reads its counters when it builds a report;
  - retries a failed report after 5 s;
  - sends the open hour before any backlog.

  Together with the 30 s report interval, this blocks a User on every Bridge within about 30 s of running out on a healthy fleet, instead of the hour ADR 0003 accepted.
- **New periods are noticed.** Blocked Devices produce no usage, so nothing would otherwise trigger a re-check. The Coordinator re-evaluates each User when their new Quota Period starts. Refill expiry never changes a state.
- **Throttling is enforced with nftables:** two named `limit` objects per throttled Device (up and down), keyed on its address, at the top of `forward` in `table inet ways`. `rate` is in bytes per second of inner traffic.
  - **Swapped with each snapshot:** the limits are replaced in the same transaction, accepting about 1 s of extra burst per reload.
  - **Measured cost:** under 1 KiB per Device and no measurable CPU at 500 Devices. Only throttled Devices' packets pay ([research](https://github.com/alakiani/ways/issues/69)).
- **Privacy:** the Coordinator sees running totals about every 30 s on the wire, and could difference them into activity at that resolution. It stores nothing finer than hourly, and nothing here changes that.

## Considered options

- **Calendar months**, UTC or Solar Hijri, instead of a Renewal Day. Rejected because the owner wanted a plan-like month per User. UTC months were the cheapest option; Solar Hijri months don't line up with any stored resolution.
- **Remedies that zero Usage.** Rejected because they would make history untrue. Additive records keep Usage exact and show each remedy.
- **A 10 s report interval, or a byte budget kept on each Bridge.**
  - The interval was rejected because it triples Coordinator writes for little gain over ~30 s.
  - The byte budget was rejected because it splits a User's Quota across Bridges, makes snapshots churn, and enforces a stale budget when a Bridge is offline.
- **Throttling with tc.**
  - **HTB on `ways0` or an ifb** puts every Device's traffic through one qdisc lock.
  - **`tc police` with a small burst** collapsed TCP in the lab.

  The download side may move to HTB later if policing feels bad to Users, because it queues instead of dropping.

## Consequences

- **Overshoot is still possible.**
  - A Bridge that can't reach the Coordinator keeps its last snapshot: blocks it already has stay, and remedies wait until it reconnects.
  - A Bridge that never received a block lets traffic through, and that usage counts later, so a period total can exceed 100%.
  - Late backlog is counted in the hour it happened, so it can briefly re-block a User after a remedy.
- **Out of scope:** paid plans, tiers, resellers, and notifications outside the panel.
