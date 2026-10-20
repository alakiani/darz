# DPI-resistant transports for Node-to-Node Links

Research for issue #3. Researched 2026-10-14. Covers only **Links** between **Nodes**
(Bridge to Bridge, Bridge to Exit) that cross the filter: one Node domestic in Iran, one abroad.
The Device-to-Bridge hop (stock WireGuard) is out of scope.

Evidence labels used below:

- **[measured]**: a peer-reviewed paper or a research group's own measurement.
- **[maintainer]**: project docs or source code, describing the tool itself. This is not evidence about Iran.
- **[anecdotal]**: forum or field reports from individual users, mostly net4people/bbs. Usually one person on one ISP.
- **[vendor]**: a VPN company's blog. Low trust.

Every claim about what passes in Iran has a date. Iranian filtering changed several times in
2025 and 2026, so treat any claim older than about 8 weeks as stale.

---

## 1. The situation a Link has to survive (dated)

Before the transport choice, these facts about the network decide most of the outcome.

1. **2026 was a year of shutdowns and an allowlist model.** There was a near-total shutdown from
   2026-01-08, then partial "allowlist" access from about 2026-01-26. A second shutdown started
   2026-02-28 and partial restoration began 2026-05-26
   ([Aceto et al., arXiv 2603.28753, Mar 2026](https://arxiv.org/html/2603.28753v1) [measured];
   [arXiv 2605.00187](https://arxiv.org/html/2605.00187) [measured];
   [Filterwatch, 2026-01-28](https://filter.watch/english/2026/01/28/network-monitoring-january-2026-from-regional-disuptions-to-total-blackout-and-whitelisted-access/)).
   During a full shutdown, no transport works. dnstt over UDP/53 was the only method the community
   reported working in the February 2026 shutdown
   ([Lantern corpus findings](https://corpus.lantern.io/findings/) [anecdotal, aggregated]).
2. **A domestic Node's source IP matters more than its transport.** UCSC measured, from a
   datacenter VPS inside Iran, that the outbound **source-IP allowlist** let only about 2,000 Iranian
   IPs (0.017%) reach abroad before 2026-05-26 and about 8,000 (0.07%) after. They also found that
   "the data center ISPs (including the one where our Iran host is situated) continue to have
   restrictions" ([UCSC RAND Lab, 2026-06-18](https://randlab.engineering.ucsc.edu/blogs/iran-allowlist/);
   [net4people #630](https://github.com/net4people/bbs/issues/630)) [measured].
   - Inbound traffic from foreign IPs reached the Iranian VPS from only about 2.8% of tested /24s.
   - **Consequence:** a Bridge in an Iranian datacenter that is not on the allowlist cannot reach
     any Exit, whatever the transport. Which datacenter and IP range WAYS uses is a survivability
     decision before it is a transport decision.
3. **QUIC has been blocked outright since June 2025.**
   - From before the June 2025 shutdown and continuing after it, 100% of QUIC Initial packets were
     dropped, on 9,000 of 9,000 domains
     ([Niere et al., "Insights into an Iranian Internet Shutdown", FOCI 2026](https://www.petsymposium.org/foci/2026/foci-2026-0016.pdf)) [measured].
   - QUIC anomalies appeared again on 2026-01-02 ([Aceto et al.](https://arxiv.org/html/2603.28753v1)) [measured, via OONI].
   - On 2026-05-31, IPv6 and HTTP/3 were reported blocked and UDP "actively disrupted"
     ([Al Jazeera, 2026-05-31](https://www.aljazeera.com/amp/news/2026/5/31/iran-reinstates-some-internet-access-but-restrictions-remain-for-most)) [press].
4. **UDP is filtered at scale.**
   - IRBlock found about 5M Iranian IPs whose UDP traffic was dropped, measured from Dec 2024 to
     Jan 2025 with QUIC-shaped probes. Its authors say that because payloads are encrypted, the
     filter relies on "packet size, port number, or traffic patterns"
     ([Tai et al., IRBlock, USENIX Security 2025](https://www.usenix.org/system/files/usenixsecurity25-tai.pdf)) [measured].
   - After the June 2025 shutdown, DNS over UDP lost packets for 89% of domains, attributed to
     general UDP instability ([FOCI 2026](https://www.petsymposium.org/foci/2026/foci-2026-0016.pdf)) [measured].
5. **Iran runs a protocol allowlist.**
   - **2020:** on TCP 53, 80 and 443, for some IP ranges, a flow is dropped unless its first two
     client packets look like DNS, HTTP or TLS
     ([Bock et al., FOCI 2020](https://www.usenix.org/system/files/foci20-paper-bock.pdf);
     [Geneva write-up](https://geneva.cs.umd.edu/posts/iran-whitelister/)) [measured].
   - **2023 to 2025:** two Iranian ASes drop non-matching traffic after about 6 packets, regardless
     of destination. In the same study, a TLS-ClientHello prefix was blocked within 72 h while a
     plain HTTP GET prefix survived 4 weeks
     ([Alaraj & Wustrow, ASIA CCS 2025](https://dl.acm.org/doi/10.1145/3708821.3733879), read via
     the [Lantern summary](https://corpus.lantern.io/papers/2025-alaraj-iran-refraction/); I could
     not fetch the full text) [measured, secondhand].
   - **June 2025:** "all other protocols we tried (including common VPN protocols) failed silently";
     only DNS/53, HTTP/80 and HTTPS/443 passed
     ([Aryapour, arXiv 2507.14183](https://arxiv.org/html/2507.14183v1)) [measured, single author].
6. **Blocking differs by ISP.** Over Jul 2023 to Feb 2025:
   - TCI (AS58224) injects packets.
   - MCI (AS197207) blocks proxy IPs **in proportion to traffic volume**.
   - Parsonline and Shatel showed no proxy blocking.

   ([Alaraj & Wustrow 2025](https://corpus.lantern.io/papers/2025-alaraj-iran-refraction/)) [measured, secondhand].
   A heavy-traffic Bridge to Exit Link is exactly the high-volume flow MCI-style blocking targets.
7. **Status of evasion tricks, summer 2026 [anecdotal].**
   - 2026-06-10: the DPI reassembles TCP fragments to read the SNI
     ([net4people #628](https://github.com/net4people/bbs/issues/628)).
   - Around 2026-07-27: TLS ClientHello fragmentation stopped working entirely
     ([#640](https://github.com/net4people/bbs/issues/640), opened 2026-07-31).
   - SNI spoofing worked for a few days in April 2026, then the gateways it relied on were put
     behind a validating "national NAT"
     ([#626](https://github.com/net4people/bbs/issues/626);
     [Al Jazeera, 2026-04-20](https://www.aljazeera.com/news/2026/4/20/iran-expands-limited-internet-access-but-restrictions-remain-for-most)).

I found no public Iran-specific report dated after 2026-09-17 on net4people, and nothing for
October 2026. Section 7 lists the open questions.

---

## 2. Comparison table

Survivability ratings are for a Link crossing the filter in summer 2026. Overhead is per packet with
an IPv4 outer header. It is my arithmetic from the header formats, not a measurement, unless a source
is cited.

| Transport (carrier for WireGuard) | Iran track record, dated | Main detection signal | Mitigation | Per-packet overhead vs plain WG (60 B) | Throughput/latency cost | Go/Ubuntu maturity |
|---|---|---|---|---|---|---|
| **Plain WireGuard (UDP)** | Blocked to foreign IPs: Oct 2022 ([#140](https://github.com/net4people/bbs/issues/140)); "blocked" by Jul 2025 ([#499](https://github.com/net4people/bbs/issues/499)); blackholed right after the handshake, Jun 2026 ([#628](https://github.com/net4people/bbs/issues/628)) [anecdotal]. UDP drops at scale [measured, IRBlock]. | Fixed message types 1 to 4, fixed handshake sizes 148/92 B, UDP | None by design: "WireGuard does not focus on obfuscation" ([wireguard.com](https://www.wireguard.com/known-limitations/)) | baseline | baseline | Kernel module plus `wgctrl`/`wireguard-go`, all first-class |
| **GRE (proto 47)** | "GRE / IPSec didn't lasted so long", Jul 2025 ([#499](https://github.com/net4people/bbs/issues/499)) [anecdotal]. UCSC ran source-spoofed GRE to an Iranian datacenter VPS in Jun 2026 as a management path, but does not say whether GRE as such is filtered ([UCSC](https://randlab.engineering.ucsc.edu/blogs/iran-allowlist/)) | Not TCP or UDP at all, so it fails any protocol allowlist; cleartext inner headers | None; it adds no encryption, so WG must run inside | +24 B | About none (kernel) | Kernel; netlink from Go (`vishvananda/netlink`) |
| **GRE over 6to4 (ip6gre over sit, proto 41)** | Popular Iran-to-abroad "local IPv6" recipe in Persian guides ([idaniali](https://github.com/idaniali/6to4-GRE6-tunnel), [raminol12](https://github.com/raminol12/6to4)) [maintainer]; no dated survival data found | Proto 41 is trivially classifiable | Some ranges are filtered by IPv4 TCP/UDP port and miss non-TCP/UDP protocols. That is my inference, not shown | +64 B (20 IPv4 + 40 IPv6 + 4 GRE) | About none (kernel) | Kernel; netlink |
| **AmneziaWG 2.0** (WG fork: random headers, padding, junk and decoy packets) | "AmneziaWG and WireSock reportedly work", 2026-06-25 [anecdotal]; contradicted the next day ([#628](https://github.com/net4people/bbs/issues/628)). Amnezia says it keeps users connected on Irancell and Hamrah-e Avval ([Amnezia blog, May 2026](https://amnezia.org/blog/internet-censorship-in-iran-q1-2026-and-forecast)) [vendor] | Still UDP, so it is exposed to blanket UDP drops and to volume and IP reputation | H1 to H4 random types, S1 to S4 size padding, I1 to I5 decoy packets (QUIC- or DNS-like), Jc junk; v3.1 adds padding and timer randomisation ([docs](https://docs.amnezia.org/documentation/amnezia-wg)) [maintainer] | +0 to S4 bytes per data packet; junk only at handshake | Near WG; UDP, no TCP-over-TCP | `amneziawg-go` (MIT, Go, pushed 2026-09-20) embeds like `wireguard-go`; kernel module via PPA/DKMS, Ubuntu 26.04 not stated ([repo](https://github.com/amnezia-vpn/amneziawg-linux-kernel-module)) |
| **VLESS + REALITY over TCP/443 (Xray-core), carrying WG UDP** | Blocked on MCI after 1 to 2 weeks per IP, 2023 ([#253](https://github.com/net4people/bbs/issues/253)); RST right after the handshake, then "working again for ~3 days with well chosen SNIs", Jun 2026; a REALITY IP blocked after 4 days ([#628](https://github.com/net4people/bbs/issues/628)); "REALITY works but requires periodic IP rotation", 2026-08-03 ([#640](https://github.com/net4people/bbs/issues/640)) [anecdotal] | Server IP reputation and volume; TLS-in-TLS when the inner traffic is TLS; SNI and IP mismatch | Borrows a real site's TLS 1.3 handshake, so active probes see the real site; uTLS browser fingerprint ([REALITY README](https://github.com/XTLS/REALITY)) [maintainer]. The inner WG is not TLS, so no TLS-in-TLS signature | About +40 to 50 B (TCP 40 vs UDP 8, TLS record about 22, VLESS UDP framing 2) | TCP head-of-line blocking; TCP-over-TCP for inner TCP ([WireGuard on TCP](https://www.wireguard.com/known-limitations/)) | Xray-core is Go, MPL-2.0, very active (pushed 2026-10-14). Embeddable but the API is undocumented; mostly driven by JSON config |
| **VLESS/WS or XHTTP + TLS via CDN (Cloudflare)** | Worked for about a week, then domain blocked in under 24 h (DNS to 10.10.34.34 plus SNI RST), Jun 2026 ([#628](https://github.com/net4people/bbs/issues/628)); all Cloudflare configs reported blocked on MCI, 2025-07-04 ([#489](https://github.com/net4people/bbs/issues/489)) [anecdotal]. During allowlisting, a non-allowlisted SNI got about 6 packets ([#626](https://github.com/net4people/bbs/issues/626)) [anecdotal] | SNI blocklist or allowlist; WS opens many TCP connections; up/down ratio | CDN IPs are costly to block wholesale; XHTTP padding and split up/down paths ([XHTTP](https://github.com/XTLS/Xray-core/discussions/4113)) [maintainer] | As REALITY plus HTTP framing; WS frame 2 to 14 B | Extra CDN hop; CDN idle and stream limits | Same Xray-core; plus a CDN account and domain rotation |
| **WireGuard over WebSocket/TLS (wstunnel, Backhaul wss)** | No Iran-specific dated data for wstunnel. Backhaul (Iranian-made) widely used Iran-to-abroad, but no survival data [maintainer] | Own domain's SNI (blocklist or allowlist); generic TLS server answers probes; volume | Real TLS; can sit behind nginx or a CDN; mTLS | About +45 to 60 B | TCP-over-TCP; wstunnel claims one connection can saturate 1 Gbit ([README](https://github.com/erebe/wstunnel)) [maintainer] | wstunnel is Rust (BSD-3), so subprocess only; Backhaul is Go but AGPL-3.0 and last pushed 2025-09-04 |
| **QUIC/HTTP3 carriers: Hysteria2, MASQUE CONNECT-UDP, TUIC, WebTransport** | QUIC 100% blocked since Jun 2025 [measured, FOCI 2026]; HTTP/3 blocked, May 2026 [press]; Hysteria2 with obfs "blocked on most ISPs depending on destination IP", Jul 2025 ([#489](https://github.com/net4people/bbs/issues/489)) [anecdotal] | QUIC Initial packets are dropped; Salamander-obfuscated traffic is random UDP | Salamander or Gecko obfuscation, port hopping ([Hysteria docs](https://v2.hysteria.network/docs/advanced/Full-Server-Config/)) [maintainer] | About +35 to 50 B (QUIC short header plus AEAD tag plus datagram frame; Salamander +8) | Fast on lossy links (Brutal congestion control); no TCP-over-TCP | Hysteria is Go/MIT (v2.13.0, 2026-10-05); `quic-go/masque-go` (MIT) implements RFC 9298 |
| **Fake-TCP / raw TCP: phantun, udp2raw, paqet** | paqet "worked reliably" Zitel to Hetzner for a week, Jan 2026 ([#570](https://github.com/net4people/bbs/issues/570)) [anecdotal]; no other Iran data | Not real HTTP/TLS, so it fails the protocol allowlist on 80/443; odd TCP state | Bypasses the kernel TCP stack; paqet runs KCP over hand-built TCP packets ([paqet](https://github.com/hanselime/paqet)) [maintainer] | phantun +12 B, udp2raw +16 B over UDP ([phantun](https://github.com/dndx/phantun)) [maintainer] | phantun 1.2 Gbit/s vs 2.37 Gbit/s direct, 1 stream on AWS (2022, self-reported) | paqet is Go/MIT but "active development, APIs may change", needs root and pcap; phantun is Rust |

---

## 3. Detection signals, and which transports answer them

| Signal | Evidence it is used in Iran | Hurts | Answered by |
|---|---|---|---|
| Non-TCP/UDP IP protocol (47, 41) | Implied by the allowlist on ranges where it applies [measured 2020, 2025] | GRE, 6to4 | Nothing; carry them only on Links that do not cross the filter |
| QUIC Initial / UDP blocking | 100% QUIC drop since Jun 2025 [measured]; about 5M IPs with UDP drops [measured] | Plain WG, Hysteria2, MASQUE, AmneziaWG | TCP carriers |
| First-packets protocol allowlist (about 2 to 6 packets) | 2020 and 2023 to 2025 [measured] | Fully encrypted or random protocols (paqet, phantun, Shadowsocks-style) | Anything that starts with a real TLS ClientHello or HTTP request: REALITY, WS/TLS, XHTTP |
| Handshake fingerprint (WG type bytes and sizes) | WG blackholed after the handshake, Jun 2026 [anecdotal] | Plain WG | AmneziaWG H/S/I parameters; any TLS wrapper |
| SNI blocklist, and an SNI allowlist during restricted phases | SNI RST and DNS poisoning to 10.10.34.34 [measured]; 6-packet cap for grey SNIs, 2026 [anecdotal] | Own-domain WS/TLS, CDN fronting | REALITY with an SNI of a large foreign site the censor keeps reachable; domain rotation |
| Fragment reassembly | Jun to Jul 2026 [anecdotal] | ClientHello-fragmentation tricks | Do not depend on fragmentation |
| Active probing | REALITY's design target; not confirmed in Iran. IODA records "active-probing responses", but that is IODA's own measurement method, not censor probing | Generic TLS proxies with self-signed certs or a 404 | REALITY forwards probes to the real site; a WS server behind a real nginx site |
| Volume and up/down ratio per IP | MCI blocks in proportion to volume, 2023 to 2025 [measured, secondhand]; ratio suspected in Jun 2026 [anecdotal] | **Every** transport. A Bridge to Exit Link is a fat, symmetric, long-lived flow | IP rotation, spreading load over several Links and IPs, cover traffic. Nothing fully answers this |
| Source-IP allowlist on domestic datacenters | Measured Jun 2026 [UCSC] | Every transport | Pick an allowlisted domestic datacenter and IP |

The Geedge Networks firewall source leak (USENIX Security 2026,
[net4people #653](https://github.com/net4people/bbs/issues/653)) is tagged Iran. I did not verify
whether the leaked equipment is deployed in Iran.

---

## 4. Throughput and latency cost

No public source measures any of these wrappers on an Iranian path. The numbers below are general
properties, plus field reports on the path itself.

- **The path itself, not the wrapper, sets the ceiling.**
  - Upload to foreign VPSs was capped around 1 to 2 Mbps, 2025-07-01
    ([#489](https://github.com/net4people/bbs/issues/489)) [anecdotal].
  - Under 1 Mbps upload, with hybrid Iranian/foreign Node chains "somewhat better", 2025-07-13
    ([#489](https://github.com/net4people/bbs/issues/489)) [anecdotal].
  - About 100 KB/s peaks during allowlisting, 2026 ([#626](https://github.com/net4people/bbs/issues/626)) [anecdotal].
- **UDP carriers (AmneziaWG, Hysteria2):** essentially WG speed. Hysteria's Brutal congestion
  control is built to push through loss
  ([docs](https://v2.hysteria.network/docs/advanced/Full-Server-Config/)) [maintainer].
- **TCP carriers (REALITY, WS, XHTTP, wstunnel):**
  - One lost packet stalls every flow multiplexed on that connection (head-of-line blocking).
  - Inner TCP over outer TCP gives "classically terrible network performance" under loss
    ([WireGuard](https://www.wireguard.com/known-limitations/)) [maintainer].
  - On a clean path the cost is small. On a lossy, throttled Iranian path, expect noticeably worse
    goodput than UDP. That is inference; I found no Iran measurement.
- **MTU:** set the WG MTU to about 1420 minus the carrier's overhead, or fragmentation will hurt.
  Over 6to4+GRE, use about 1356. phantun recommends 1428 for WG on its own
  ([phantun](https://github.com/dndx/phantun)) [maintainer].
- **CPU:** TLS adds a second AEAD on top of WG's ChaCha20. That is negligible at the Link speeds
  Iran allows.

---

## 5. Maturity for a Go Agent on Ubuntu 26.04

| Component | Go-native? | Licence (checked 2026-10-14 via GitHub API) | Unattended operation | Notes |
|---|---|---|---|---|
| WireGuard kernel plus `wgctrl-go` | Yes | MIT | systemd-networkd or netlink | Baseline |
| GRE / sit / ip6gre | Yes, via netlink | n/a (kernel) | Yes | No crypto; always run WG inside |
| `amneziawg-go` | Yes, an in-process library like `wireguard-go` | MIT | Yes, as a TUN in the Agent | The kernel module needs DKMS against the 26.04 kernel (unverified). Prefer userspace |
| Xray-core (REALITY, XHTTP, WS) | Yes, Go; embeddable via `core.New(config)`, but the API is undocumented and churns (release v26.3.27, 2026-03-27; repo pushed daily) | MPL-2.0 (file-level copyleft, so linking it is fine) | Yes, as an embedded library or a systemd subprocess with a generated JSON config | The lowest-risk way to drive it is a subprocess plus a JSON config the Agent generates |
| sing-box | Go | **GPL-3.0 plus a naming restriction** | Yes | Linking it would make the Agent GPL. Use it as a subprocess or avoid it |
| Hysteria2 | Go | MIT | Yes | Low value in Iran while QUIC and UDP are blocked |
| `quic-go/masque-go` | Go | MIT | Library | RFC 9298 CONNECT-UDP only; QUIC is blocked in Iran |
| wstunnel | Rust | BSD-3 | Static binary, systemd | Subprocess only |
| Backhaul | Go | AGPL-3.0, last push 2025-09-04 | Yes | Licence and staleness count against it |
| paqet | Go | MIT | Root, pcap, iptables NOTRACK rules | "APIs may change", so watch it, do not depend on it |

Whether Ubuntu 26.04 support is documented: none of the projects above states it. Everything Go or
static-Rust above has no distro dependency. Only the AmneziaWG kernel module depends on the kernel
version.

---

## 6. Recommendation (survivability first, performance second)

The Agent should treat the transport of a Link that crosses the filter as **pluggable, with several
transports live per Node pair and automatic failover**. Nothing in Iran survives for long unchanged:
REALITY IPs last days to weeks, and CDN domains last about one day to one week (summer 2026).
Links that do **not** cross the filter (abroad to abroad, domestic to domestic) should stay plain
WireGuard, optionally over GRE, for full performance.

Ranking for Links that cross the filter:

1. **WireGuard inside VLESS + REALITY over TCP/443 (Xray-core, driven as a subprocess)**
   - **Why first:** it is the only transport with an Iranian report saying it works dated after
     fragmentation died (2026-08-03, [#640](https://github.com/net4people/bbs/issues/640)) [anecdotal].
     It starts with a genuine TLS 1.3 ClientHello, so it passes the protocol allowlist. It answers
     active probes with a real site, and carrying WG instead of TLS avoids the TLS-in-TLS signature.
   - **Cost:** TCP-over-TCP and head-of-line blocking, and an IP that needs rotation every few days.
     The Coordinator must therefore manage a pool of Exit IPs.
2. **WireGuard inside XHTTP or WS + TLS via a CDN (Xray-core)**, as the fallback when Exit IPs are
   burned.
   - **Why second:** CDN IPs are expensive for the censor to block, but domains die within about
     24 h once flagged (Jun 2026) and SNI allowlisting can starve them.
   - **Requires:** rotating domains, and has lower throughput.
3. **AmneziaWG 2.0/3.x (in-process `amneziawg-go`)**, as the performance transport whenever UDP
   passes.
   - **Why third:** it is the best performance of the obfuscated options and the simplest to embed.
     Iran evidence is thin and contradictory (Jun 2026), and blanket UDP disruption ([measured])
     makes it the first casualty of every crackdown.
   - **Policy:** probe it continuously and prefer it when it works.
4. **Plain WireGuard, GRE, GRE over 6to4**, only for Links that do not cross the filter, or on a
   domestic Node whose datacenter IP is known to pass them. Reported short-lived across the filter
   since 2022 and 2025 [anecdotal]. GRE and 6to4 add no security and fail any protocol allowlist.
5. **QUIC carriers (Hysteria2, MASQUE, WebTransport)**: do not build on them for crossing Links.
   QUIC has been 100% blocked since June 2025 [measured]. Revisit only if OONI's HTTP/3 tests show
   QUIC returning.

Watch list, not recommended: **paqet** (raw-TCP KCP, one good Iran report from Jan 2026, immature),
and **dnstt** (survived the Feb 2026 shutdown, but kbit/s, so only a lifeline).

Choices that matter as much as the transport:

- **Domestic Node placement.** Rent the domestic Bridge in a datacenter and IP range that is on the
  outbound allowlist. Verify this per range ([UCSC, Jun 2026](https://randlab.engineering.ucsc.edu/blogs/iran-allowlist/)).
- **Volume shaping.** Spread a Bridge's traffic over several Links and Exit IPs. Volume-proportional
  IP blocking (MCI) punishes one fat Link.
- **Direction.** In 2026, foreign hosts often could not initiate connections into Iran
  ([#626](https://github.com/net4people/bbs/issues/626); [UCSC](https://randlab.engineering.ucsc.edu/blogs/iran-allowlist/)).
  The domestic Bridge should always dial out to the Exit (client role), never listen.

---

## 7. Caveats and gaps

- No primary source after 2026-09-17 covers Iran transports, and nothing covers October 2026. All
  per-transport survival evidence for 2026 is anecdotal (net4people), from a handful of users.
  Measured evidence covers QUIC, UDP, the protocol allowlist and the source-IP allowlist, not
  specific tools.
- No source separates datacenter-to-datacenter Links from residential clients for any specific
  transport. Most reports are residential client to foreign server. Datacenter behaviour can be
  better (some datacenter IPs reportedly pass "easy-to-detect protocols", Jul 2025,
  [#499](https://github.com/net4people/bbs/issues/499)) or worse (UCSC, Jun 2026).
- The Alaraj & Wustrow (2025) figures come from a corpus summary; I could not retrieve the full
  paper text.
- The overhead figures in section 2 are arithmetic estimates, not measurements.
- Recommended validation: before committing, run a two-week A/B test from at least two domestic
  datacenters (different ASes) to two Exits. Run REALITY, XHTTP-over-CDN and AmneziaWG at the same
  time and log time-to-block per IP.
