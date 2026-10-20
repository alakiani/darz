# Path selection models: central computation vs. routing protocols over Links

Research for issue #6. Question: for a mesh of fewer than about 50 Nodes joined by
measured Links, should the Coordinator compute Paths and push them to Agents, or should
Nodes run a routing protocol (Babel, BGP, OSPF, or an overlay-native scheme) over the
Links and converge on their own?

Every claim below is cited to a primary source: vendor docs, source code, an RFC, or a
daemon manual. The research was done on 2026-10-14. Items marked **Unverified** could not
be confirmed from a primary source. To keep WAYS vocabulary intact, other products'
traffic-forwarding servers are called "forwarding servers" here, whatever the product
calls them.

## 1. What comparable systems do

### Tailscale

- **Model: centrally distributed inputs, client-side decisions.** The coordination
  server sends each client a DERP map describing the DERP servers available to it. The
  client "selects a home DERP server based on latency information" and reports its
  choice back, and the server shares it with the other devices. If the home region
  becomes unreachable, "the Tailscale client selects the next closest region."
  <https://tailscale.com/kb/1232/derp-servers>
- **Hysteresis on the home DERP region (source code).** In
  `addReportHistoryAndSetPreferredDERP`, the client keeps its old region unless the best
  region is at least `preferredDERPAbsoluteDiff = 10ms` faster *and* faster than 2/3 of
  the old region's latency. A region counts as present if a frame arrived within
  `PreferredDERPFrameTime = 8s`. The latency history is the lowest sample per region over
  about 5 minutes (`fullReportInterval = 5m`). The home region is always probed, with
  extra retries, to avoid flapping.
  <https://github.com/tailscale/tailscale/blob/main/net/netcheck/netcheck.go>
- **Choosing between direct UDP paths (source code).** `betterAddr` scores candidate
  paths 0-100 by relative latency and prefers direct paths over Geneve-encapsulated
  forwarded paths. It does not switch "if the latency improvement is less than 1%; we
  want a bit of 'stickiness' (a.k.a. hysteresis) to avoid flapping". Constants in
  `magicsock.go` include `heartbeatInterval = 3s`, `trustUDPAddrDuration = 6500ms`,
  `upgradeUDPDirectInterval = 1m`, `goodEnoughLatency = 5ms` and
  `sessionActiveTimeout = 45s`.
  <https://github.com/tailscale/tailscale/blob/main/wgengine/magicsock/endpoint.go>,
  <https://github.com/tailscale/tailscale/blob/main/wgengine/magicsock/magicsock.go>
- **Exit nodes.** Exit nodes are chosen manually by default. With
  `--exit-node=auto:any`, "Tailscale uses latency and performance information to select a
  recommended standard exit node", and the client "automatically switches to the
  suggested exit node when available exit nodes or network conditions change". The docs
  do not say whether the client or the server computes the suggestion. The netcheck
  source says the per-region best latencies are used for "suggesting an exit node".
  <https://tailscale.com/kb/1392/auto-exit-nodes>
- **Subnet-router failover is central.** By default, "Active-passive failover selects the
  eligible connector that joined the tailnet first". Regional routing uses the latencies
  that clients report to DERP. Failover after `tailscale down` "can take about 15
  seconds", and longer after a partition.
  <https://tailscale.com/docs/features/route-selection>
- **Coordinator down.** "Tailscale does not route any traffic through the coordination
  server". Devices keep communicating "until one of the device's keys expires", and
  firewall rules stay cached and enforced. New devices cannot join and rules cannot
  change. <https://tailscale.com/docs/reference/coordination-server-down>

### Headscale

- Headscale reimplements the Tailscale coordination server, so client-side path logic
  is the same as Tailscale's. The DERP map is configured centrally through YAML `paths`
  or remote `urls`, and the embedded DERP server is off by default.
  <https://headscale.net/stable/ref/derp/>
- HA subnet routes are enabled when "at least two nodes advertise the same prefix". When
  one router node goes offline, another serves the same routes. The server tracks which
  route is "Serving (Primary)". The docs give neither the health signal nor the failover
  timing. <https://headscale.net/stable/ref/routes/>

### Netbird

- **Model: central.** The Management service is "the central coordination component". It
  pushes network changes to clients over an open channel. The Signal service and the
  forwarding service make no routing decisions.
  <https://docs.netbird.io/about-netbird/how-netbird-works>
- **Forwarding-server selection is a first-to-connect race, with no metric.**
  `PickServer` dials up to `maxConcurrentServers = 7` server URLs in parallel, and the
  first success wins. Later successes are closed. The timeout is 30s.
  <https://github.com/netbirdio/netbird/blob/main/shared/relay/client/picker.go>
- **Unverified:** what Netbird does when Management is down. The docs say nothing about
  it.

### Nebula

- **Model: static config plus a discovery server.** Each host lists in its config which
  hosts may forward for it, and those hosts must opt in. The list is reported to the
  lighthouse, which hands it to Nodes that are handshaking.
  <https://nebula.defined.net/docs/config/relay/>
- **No metric.** Its start-up routine sends a request to every known forwarding host in list
  order, and whichever is established first carries the handshake. It does no latency
  or loss ranking. <https://github.com/slackhq/nebula/blob/master/relay_manager.go>
- The lighthouse is "responsible for keeping track of all of the other Nebula hosts, and
  helping them find each other".
  <https://nebula.defined.net/docs/guides/quick-start/>
  **Unverified:** tunnel behaviour when the lighthouse is down. The docs say nothing
  about it.

### ZeroTier

- **Model: fixed root hierarchy plus opportunistic direct paths.** "Nodes start with no
  direct links to one another, only upstream to roots". A sender with no direct path
  "sends it upstream to R (a root)". Communication falls back to "(slower)" forwarding
  when no direct path can be set up, and "Direct connection attempts continue forever
  on a periodic basis." <https://docs.zerotier.com/protocol/>
- User-defined roots ("moons") are used "if they look faster or if nothing else is
  available". <https://docs.zerotier.com/roots>
- **Unverified:** the exact metric and the controller-outage behaviour. The protocol page
  says only that credentials travel node to node, so nodes need not "constantly consult
  the controller".

### Tor

- **Model: central directory consensus, client-side path computation.** Clients build
  circuits once they have "enough directory information". Selection uses "the bandwidth
  value declared in the consensus". Some circuits "are built preemptively because we
  expect to need them later".
  <https://spec.torproject.org/path-spec/general-operation.html>
- **Latency handling is statistical and adaptive.** Clients fit a Pareto distribution to
  the last 1000 circuit build times, need at least 100 samples, and set the timeout at
  the 80th percentile (abandoning at the 99th). If more than 18 of the last 20 circuits
  time out, the client resets the timeout to 60s; it treats this as network liveness.
  <https://spec.torproject.org/path-spec/learning-timeouts.html>

### Pattern across all six

None of these systems runs a classic routing protocol between its infrastructure
servers. Each distributes membership and candidates centrally (DERP map, Management
state, lighthouse, roots, consensus). The endpoint then decides among a short candidate
list using measured latency or a connect race, with explicit stickiness: Tailscale has a
10ms / 2/3 rule for DERP and a 1% rule for direct paths, and Tor builds circuits
preemptively. Where one choice must be coordinated (Tailscale and Headscale
subnet-router primaries), the server decides, and failover takes around 15s.

## 2. Routing protocols over tunnels

### Babel (RFC 8966, babeld, BIRD, FRR)

- **Convergence.** The suggested defaults are a 4s multicast Hello, IHU every 12s,
  Update every 16s, IHU hold time 42s, and route expiry 56s. On wired or tunnel links,
  the 2-out-of-3 rule declares the link down after two missed Hellos, so detection takes
  about 8-12s at defaults (RFC 8966 App. A.2.1, App. B). Both settings can be tuned.
  <https://www.rfc-editor.org/rfc/rfc8966.html>
- **Loop freedom without global state.** The feasibility condition (D(B) < FD(A)) and
  sequence numbers keep the forwarding graph loop-free during convergence (RFC 8966
  sec. 2.4, 2.5).
- **Latency-aware metric.** RFC 9616 adds RTT via timestamps on Hello/IHU, with
  smoothing α = 0.836. The cost is nominal below `rtt-min = 10ms`, rises linearly up to
  `rtt-max = 120ms`, and adds `max-rtt-penalty = 150` above that. The RFC itself names
  tunnels and VPNs as the motivation and recommends the RFC 8966 A.3 hysteresis.
  <https://www.rfc-editor.org/rfc/rfc9616.html>
  In babeld, timestamps default to on "for tunnel interfaces", and `max-rtt-penalty`
  defaults to 96 on tunnels. <https://github.com/jech/babeld/blob/master/babeld.man>
  In BIRD, "A tunnel is like a wired interface, but turns on RTT-based metrics with a
  default cost of 96". <https://bird.network.cz/?get_doc&v=20&f=bird-6.html>
  FRR babeld has `babel enable-timestamps`, rtt-min/max and max-rtt-penalty.
  <https://docs.frrouting.org/en/latest/babeld.html>
- **Loss-aware metric.** ETX (cost = 256/(α·β)) is specified for wireless links. Wired
  links use binary 2-out-of-3 (RFC 8966 App. A.2). Loss therefore counts on a tunnel
  only if the interface is treated as lossy, through babeld's `link-quality`.
- **Bandwidth.** No standard metric exists for it.
- **Hysteresis is required by the spec.** Route selection "SHOULD include some form of
  hysteresis" (sec. 3.6). The recommended rule is to switch "only when both m(R') < m(R)
  and ms(R') < ms(R)", where ms is a smoothed metric (App. A.3). babeld's
  `smoothing-half-life` defaults to 4s.
- **Policy routing.** Babel routes by destination by default. Source-specific routing
  (RFC 9079) forwards "according to both their destination address and their source
  address". <https://www.rfc-editor.org/rfc/rfc9079.html> Per-Device selection of an
  Exit is not something Babel expresses. You would need one prefix or table per Exit
  plus local `ip rule`s.
- **Go drivability.** No maintained Go-native Babel exists. `cunicu/go-babel` was the
  only Go implementation found and now returns 404 (checked 2026-10-14). The practical
  route is to run babeld, which is C, maintained (last push 2026-09-30) and has 454
  stars (<https://github.com/jech/babeld>), and drive it through its local config
  socket (`-g` read-only, `-G` read-write, `monitor`), or to run BIRD or FRR and drive
  them through their CLI sockets.
- **No central component.** Babel has no controller, so a Coordinator outage has no
  effect on it. The risk is the other way round: the Coordinator no longer knows what
  the network chose.

### BGP (BIRD, FRR, GoBGP)

- **Convergence.** This is slow at default timers: BIRD's hold time defaults to 240s,
  with keepalive at one third of that
  (<https://bird.network.cz/?get_doc&v=20&f=bird-6.html>). It is fast only with BFD.
  FRR bfdd defaults to 300ms rx/tx with a detect multiplier of 3, which detects failure
  in about 0.9s, and `neighbor <addr> bfd` ties BGP to it.
  <https://docs.frrouting.org/en/latest/bfd.html>
- **Metric.** The FRR best-path decision (weight, local-pref, AS path, origin, MED, IGP
  cost) has no latency input. The only RTT setting is a per-neighbour shutdown
  threshold, a session kill switch. <https://docs.frrouting.org/en/latest/bgp.html> To
  make BGP latency-aware, the Agent would have to set local-pref or MED itself. In that
  case the Agent, not BGP, is computing the Path.
- **Damping.** BGP route-flap dampening defaults to a 15-minute half-life, a suppress
  threshold of 2000, reuse at 750, and max-suppress of 60 minutes (FRR). That is far too
  coarse for failover within seconds. Graceful restart keeps stale routes for
  `restart-time` (default 120s) when a session drops.
- **Go drivability.** This is the strongest option of the four. GoBGP is a Go-native BGP
  implementation with a "Go Native BGP Library", Zebra/FIB integration and BFD docs. It
  has 4116 stars and was last pushed 2026-10-14. <https://github.com/osrg/gobgp>
- **Policy.** BGP communities and route-maps are expressive, but they express policy
  per prefix, not per Device.

### OSPF (BIRD, FRR)

- **Convergence.** At defaults, Hello is 10s and dead is 40s.
  <https://docs.frrouting.org/en/latest/ospfd.html> FRR can go to a sub-second dead
  interval with `dead-interval minimal hello-multiplier` (2-20 Hellos per second). OSPF
  can also use BFD (`ip ospf bfd`, or the BIRD `bfd` option, which is off by default).
  LSA origination is throttled to 5s by default (MinLSInterval).
- **Metric.** Cost is static per interface: BIRD's default is 10, and FRR derives cost
  from a reference bandwidth of 100 Mbit/s. Making OSPF latency-aware means the Agent
  rewrites interface costs, and every change floods LSAs and reruns SPF network-wide.
  <https://bird.network.cz/?get_doc&v=20&f=bird-6.html>
- **Go drivability.** No mature Go-native OSPF was found. You would drive BIRD or FRR.
- **Policy.** OSPF computes destination-based shortest paths only.

## 3. Comparison against the WAYS requirements

| Criterion | Central (Coordinator computes, Agent fails over locally) | Babel (babeld/BIRD, RTT on) | BGP + BFD (GoBGP/FRR) | OSPF (+BFD) |
|---|---|---|---|---|
| Failure detection | Agent's own Link probes. We pick the interval (for example 1s, down after 3 misses) | 2-of-3 Hellos: about 8-12s at defaults, tunable | BFD about 0.9s at FRR defaults. Without BFD, minutes | 40s default; sub-second with minimal dead or BFD |
| Re-route after detection | Instant if backups are already pushed. Otherwise waits for the Coordinator | Local, loop-free, a few seconds | Withdraw plus best-path: seconds | SPF throttle plus LSA flood: seconds |
| Latency-aware | Yes, any function of measured latency, loss and bandwidth | Yes (RFC 9616), bounded 10-120ms band | No (the Agent hacks local-pref or MED) | No (static cost; the Agent rewrites it and SPF reruns everywhere) |
| Loss-aware | Yes | Only through ETX or link-quality, which is meant for wireless | No | No |
| Bandwidth-aware | Yes | No | No | Static reference bandwidth only |
| Coordinator down | Agents keep the last-known candidate Paths and still fail over among them. No new Paths are computed | Unaffected | Unaffected | Unaffected |
| Abrupt filter-induced Link death | Probe loss flips the Link at once. The Coordinator can also mark filter-crossing Links of one transport as suspect fleet-wide | The Link expires after missed Hellos. There is no notion of correlated failure | BFD drops the session; fast | BFD drops the adjacency; fast |
| Per-Device policy to a chosen Exit | Native: Rules, Tags and Exit health are all in one model | Needs per-Exit prefixes or tables plus `ip rule`. Babel only supplies reachability | Communities plus route-maps; awkward per Device | Not expressible |
| Hysteresis / flap damping | Ours to write, following proven patterns (Tailscale's 10ms + 2/3 rule, Babel A.3 dual metric) | Built in (smoothed metric, `smoothing-half-life`) | Dampening is minutes-scale, far too coarse | None beyond throttles |
| Go drivability | Pure Go, no extra daemon | No Go-native implementation. Drive the babeld or BIRD socket | GoBGP is native Go | Drive BIRD or FRR |
| 3am debuggability | One computed table per Bridge, which the Coordinator can show with "why". Failure modes: stale config, a bug in our scorer | Read `babeld` dump or `birdc show route` on each Node. The decision is spread across Nodes | Mature tools (`show bgp`), but policy ends up as route-maps written by the Agent | Mature tools, but cost-rewrite storms are hard to reason about |
| New failure modes added | Coordinator bugs push bad Paths to everyone | Daemon state and kernel table fights with our policy routing; a second source of truth | Same as Babel, plus heavy configuration | Same, plus fleet-wide SPF churn |

### What each model breaks at 3am

- **Central.** If a Coordinator bug pushes a bad Path set, every Bridge is wrong at
  once. Mitigations: Agents validate pushed config and keep the previous version, a
  rollback exists, and Agents fail over locally among pushed candidates no matter what
  the Coordinator does. If the Coordinator is unreachable, nothing breaks right away,
  but Paths go stale as the filter moves, because Link measurements no longer reach a
  place that recomputes.
- **Babel.** Routes and the reasons for them are spread across Nodes. Expect questions
  like "why does Bridge A prefer Bridge B → Exit 3?", which you answer from metric dumps
  on three Nodes. The daemon's kernel route installs can fight the Agent's per-Device
  `ip rule` and tables. Upgrades of a C daemon must match Ubuntu 26.04 packaging.
- **BGP / OSPF.** Configuration drift in route-maps or interface costs written by the
  Agent. Timer mismatches (OSPF hello/dead must match on both ends). In practice the
  protocol adds a second decision engine whose metric ignores what WAYS actually
  measures.

## 4. Fit with WAYS specifics

- **Topology is tiny and shallow.** A Path is Bridge → Exit or Bridge → Bridge → Exit,
  at most two Links. With fewer than 50 Nodes, the full Bridge × Exit × intermediate set
  is at most a few thousand candidates. Computing it is trivial anywhere. Distributed
  shortest-path protocols solve a problem WAYS does not have: discovering multi-hop
  paths in large graphs without global knowledge.
- **The decision is policy plus health, not shortest path.** The Exit is chosen by
  Device Rules (CIDR sets to Tags) and health. None of Babel, BGP or OSPF can express
  "Device X's traffic to Tag Y leaves via an Exit carrying Tag Y, using the healthiest
  Path to it". All three would only supply reachability under the Agent's own logic.
- **Fast failure is a local problem.** Filter-induced Link death is abrupt, so the Node
  that sees it must react without a round trip. Every comparable system does this at the
  endpoint, choosing among pre-distributed candidates (Tailscale's DERP and direct-path
  selection, Netbird's connect race, Nebula's list, Tor's preemptive circuits).
- **Coordinator independence is already a WAYS requirement.** A precomputed ranked
  candidate list (primary plus backups per Exit) satisfies it directly. This matches
  Tailscale's documented outage behaviour: cached state keeps working, but nothing new
  is computed.

## 5. Recommendation for phase one

**Compute Paths centrally, and fail over locally among pre-pushed candidates. Run no
routing protocol in phase one.**

1. **The Coordinator** collects Link measurements (latency, loss, bandwidth, the
   crosses-filter flag) from Agents. For each Bridge and each Exit, it computes a
   ranked list of k candidate Paths (for example 3), with diverse first Links and
   transports where possible. It pushes the lists to Agents as versioned config,
   alongside Rules and Tags.
2. **The Agent on each Bridge** probes its own Links continuously. It owns the switch
   from the active Path to the next healthy candidate, and from an Exit to the next Exit
   matching the Device's Tag, without asking the Coordinator. Detection target: down
   after about 3 missed 1s probes, so reconnect within seconds.
3. **Hysteresis, borrowed from proven designs.** Switch away immediately on hard
   failure. Switch back or "upgrade" only when the new candidate is better on both the
   instantaneous and the smoothed score (Babel RFC 8966 App. A.3), and by an absolute
   and a relative margin (Tailscale: at least 10ms and below 2/3 of current). Add a
   minimum hold time on the new Path and a penalty for a Link that has flapped
   recently. Make all thresholds Coordinator-pushed parameters.
4. **When the Coordinator is down,** Agents run on their last-known candidate lists
   indefinitely and keep failing over within them. Log, and expose in the Agent status,
   the age of the config.
5. **Debuggability.** The Coordinator stores, for each Bridge, the ranked candidates and
   the score breakdown behind each one. The Agent exposes the active Path, the reason
   for the last switch, and the probe history. One place answers "why this Path".

**Revisit in phase two** if the mesh needs Bridge → Bridge chains that change faster
than the Coordinator can recompute, or must survive long Coordinator outages while
Links churn. At that point the best fit is **Babel through babeld or BIRD with RTT
metrics on tunnel interfaces**. It is the only protocol with a latency metric,
hysteresis built into the spec, and loop-free local convergence. It would supply
Bridge-to-Exit reachability under the Agent's policy routing, not replace it. Reject
OSPF, whose static costs and network-wide SPF churn fit badly. Reject BGP unless
GoBGP's Go-native embedding becomes decisive, because BGP has no latency metric of its
own.

## 6. Caveats and gaps

- Not verified from primary sources: Netbird behaviour when Management is down; Nebula
  tunnel behaviour when the lighthouse is down; ZeroTier's direct-versus-forwarded
  metric and its controller-outage behaviour; FRR's default BGP keepalive and hold
  timers (the doc page was too long to reach them, so BIRD's 240s default is cited
  instead); Tor's exact bandwidth-weighting formulas (not fetched).
- Tailscale's automatic exit-node logic lists inputs (latency, performance, location)
  but not where it is computed or how often it is re-evaluated.
- Babel convergence numbers are protocol defaults. No measurement was made on real
  filter-crossing tunnels. A short prototype comparing babeld (RTT on, 1s Hello)
  against the Agent's own probe loop on a few lab Links would validate the phase-two
  option.
- `cunicu/go-babel` returned 404 on 2026-10-14. No other Go-native Babel was found.
- Some source URLs contain banned WAYS words in their paths (Netbird's and Nebula's
  source files). They are left as-is because they are external identifiers.
