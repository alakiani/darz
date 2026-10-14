# Stock WireGuard apps: endpoint resolution and roaming per platform

Research for issue #4. The question: how does the stock WireGuard app on each
platform treat the peer `Endpoint`? The answer decides how failover between
Bridges can be designed.

Everything below comes from the WireGuard projects' own source code (read at
the commits listed under Sources), their man pages and docs, and the WireGuard
mailing list. Each code citation names the file and function.

## Short answer

- **No stock app re-resolves an endpoint hostname while the tunnel is up.**
  Every platform resolves the hostname once, when the tunnel comes up, and
  passes a bare IP to the engine. The engines (wireguard-go, WireGuardNT and the
  Linux kernel module) cannot resolve names at all.
- A hostname is resolved again only when the tunnel is **brought up again**.
  That happens when the user toggles it, when the OS restarts it (at boot,
  through Android always-on, or through iOS/macOS on-demand after the tunnel
  has stopped), or on Linux when a cron job runs the contrib
  `reresolve-dns.sh`.
- A dead endpoint is **never reported as a failure**. The tunnel stays
  "active" and handshakes are retried for about 90 s. After that the engine
  stops trying until new traffic arrives. Nothing restarts the tunnel by
  itself, so nothing triggers a new resolution.
- A config can hold **exactly one endpoint per peer**, and two peers cannot
  share an exact AllowedIPs prefix. No supported option lists several
  endpoints.
- Of the three failover strategies, only one fails over with no user action on
  every platform: **a stable IP that moves to a healthy Bridge (floating or
  anycast), combined with Bridges that share one WireGuard identity**. A
  short-TTL hostname recovers only after the user toggles the tunnel or the
  device reboots.

## Per-platform table

| | Android (GoBackend) | iOS (WireGuardKit) | macOS (WireGuardKit) | Windows (wireguard-windows + WireGuardNT) | Linux (wg-quick + kernel) |
|---|---|---|---|---|---|
| **When the hostname is resolved** | At tunnel up, in `GoBackend.setStateInternal`. Up to 10 tries, 1 s apart. A failure aborts with `DNS_RESOLUTION_FAILURE` [A1] | At tunnel start: `WireGuardAdapter.start` → `resolvePeers` → `DNSResolver.resolveSync`. A failure aborts the start [I1][I2] | Same code as iOS [I1][I2] | At tunnel-service start: `Config.ResolveEndpoints` → `resolveHostname`. 10 tries, 4 s apart (30 at boot). A failure stops the service with `ErrorDNSLookup` [W1][W2] | When `wg setconf`/`addconf` parses the config, in `parse_endpoint`: `getaddrinfo` with 15 retries by default (`WG_ENDPOINT_RESOLUTION_RETRIES`) [L1][L2] |
| **Which address is used** | First IPv4 from `getAllByName`, else the first result [A2] | First IPv4 (with `AI_ALL`), else IPv6 [I2] | Same as iOS | First IPv4, else the first IPv6 [W2] | First `getaddrinfo` result [L1] |
| **Caching** | Per `InetEndpoint` object. The cached IP is reused until 2 whole minutes have passed (`toMinutes() > 1`). The source carries `TODO(zx2c4): Implement a real timeout mechanism using DNS TTL` [A2] | Resolved IPs are kept in the `PacketTunnelSettingsGenerator` for the life of the tunnel [I1] | Same as iOS | Resolved once per service start [W1] | Resolved once per `wg set`/`setconf` call |
| **Re-resolution while up** | None. The tunnel and UI code have no network-change listener. | None for the hostname. On every network path change, `didReceivePathUpdate` runs `getaddrinfo` again on the **already-resolved IP literal**. This re-creates a NAT64 address on DNS64 networks; the original name is not looked up [I1][I3] | None. A path change only calls `wgBumpSockets` (rebind sockets and send a keepalive) [I1][I4] | None | None by default. The contrib `reresolve-dns.sh`, run from cron "every thirty seconds or so", re-runs `wg set … endpoint <hostname>` for any peer whose latest handshake is older than 135 s [L3] |
| **Network change** | No app-level handling. wireguard-go's socket follows the default network | When no network path is available, the backend is turned off (`temporaryShutdown`). When the path returns, the backend restarts **with the same stored IPs**. On a path change while up, the endpoint is set again from the stored IP, roaming is disabled and the sockets are bumped [I1] | Sockets bumped only [I1] | WireGuardNT tracks the outgoing route and replies on the interface the packet arrived on [W3] | The kernel picks a route for each packet |
| **Inbound roaming (endpoint updated from authenticated packets)** | **Disabled** once an endpoint is set (`DisableSomeRoamingForBrokenMobileSemantics`) [A3][G1] | **Disabled** by the same call, which runs on iOS only [I1][I4][G1] | Enabled (the call sits under `#if os(iOS)`) [I1] | Enabled (standard WireGuard behaviour) [P1] | Enabled. wg(8): the endpoint "will be updated automatically to the most recent source IP address and port of correctly authenticated packets" [L4] |
| **Endpoint stops responding** | The protocol timers are the same on every platform. If the Device sends traffic and gets no reply for 15 s (`KeepaliveTimeout + RekeyTimeout`), it sends a new handshake. Handshakes are retried every 5 s (plus up to 334 ms jitter) for 90 s (`RekeyAttemptTime`). Then the engine "gives up" and drops queued packets until new outgoing traffic starts another round. Keys are zeroed after 540 s [G2][G3][P2]. | Same | Same | Same | Same |
| **What the user sees** | The tunnel stays "on". Only the peer's latest-handshake time goes stale. No error is shown and nothing restarts. | Same. The VPN stays "Connected" | Same | Same. The UI shows "Latest handshake" [W4] | The latest-handshake value in `wg show` keeps growing |
| **Automatic restart / always-on** | System always-on VPN starts the service at boot or after the process dies. `onStartCommand` → `alwaysOnCallback` → `restoreState(true)` brings the last tunnel up, which resolves the name again [A4][A5]. A dead Bridge does not stop the service, so always-on does not fire. | On-demand rules (`NEOnDemandRuleConnect` on any interface, Wi-Fi, cellular or a list of SSIDs) [I5]. iOS restarts the tunnel only after it has stopped, and a dead endpoint does not stop it. | Same on-demand code, with Ethernet in place of cellular [I5] | No on-demand. The tunnel service is `StartAutomatic`, so it starts and resolves at every boot [W5] | None in wg-quick |
| **Kill switch** | With exactly one peer that has a `/0` route, the app does not call `allowFamily`. Traffic is then blocked, not leaked, while the tunnel is down [A6] | Not studied | Not studied | One peer with a `/0` AllowedIP turns on firewall rules. They block all traffic outside the tunnel, and all port 53 traffic except to the configured DNS servers [W3] | Optional PostUp rules, as documented in wg-quick(8) |
| **Multiple Endpoint lines in a peer** | The last one wins (the builder overwrites) [A7] | Rejected with `multipleEntriesForKey`. Only Address, AllowedIPs and DNS may repeat [I6] | Rejected [I6] | The last one wins (`peer.Endpoint = *e`) [W6] | One value. wg(8) defines Endpoint as one "IP or hostname" [L4] |
| **Two peers, same public key** | The parser does not check | Rejected with `multiplePeersWithSamePublicKey` [I6] | Rejected [I6] | The parser does not check | Not traced |
| **Two peers, overlapping AllowedIPs** | For the exact same prefix, the later peer takes it over and the earlier peer loses it (`insert`, `if exact { … node.peer = peer }`). Different prefix lengths: longest-prefix match [G4][P1] | Same (wireguard-go) | Same | Same cryptokey routing (WireGuardNT) [P1] | Same (kernel) [P1] |
| **AllowedIPs count limit** | WireGuard code sets no limit. Each entry becomes one `VpnService.Builder.addRoute` [A6] | WireGuard code sets no limit. A report on Apple's developer forum describes a Network Extension crashing for memory at 2048 IPv6 routes [X1] | No WireGuard limit found | Allowed IPs are de-duplicated and installed as routes. No limit is documented [W3] | None documented |

## Detail by platform

### Android (`wireguard-android`, GoBackend)

- `GoBackend.setStateInternal` resolves every peer endpoint before it builds
  the userspace config ("Pre-resolve IPs so they're cached when building the
  userspace string"). It retries 10 times with a 1 s sleep, then throws
  `DNS_RESOLUTION_FAILURE` [A1].
- `InetEndpoint.getResolved` caches the result in the object. It looks the name
  up again only when `Duration.between(lastResolution, now).toMinutes() > 1`,
  that is, after 2 whole minutes. The DNS TTL is ignored [A2]. If the user
  toggles the tunnel off and on within 2 minutes, the old IP is reused.
- At turn-on, the Go side calls
  `device.DisableSomeRoamingForBrokenMobileSemantics()` [A3]. In wireguard-go
  this sets `disableRoaming` on every peer that already has an endpoint, so
  `SetEndpointFromPacket` ignores the source address of authenticated packets
  [G1]. The client keeps sending to the IP it resolved.
- A search of the whole tree found no `NetworkCallback` or similar in the
  tunnel or UI modules. The app does nothing when the network changes.
- Always-on: `VpnService.onStartCommand` recognises a start that did not come
  from the app ("Service started by Always-on VPN feature") and calls
  `restoreState(true)` [A4][A5]. That brings the tunnel up from scratch, so the
  name is resolved again. The 2-minute cache still applies if the process
  survived.
- Mailing list: a 2018 user reported that Android kept using a cached address
  after the server's IP changed, until the app was force-stopped [M1]. The
  current code has the 2-minute cache and the TTL TODO.

### iOS and macOS (`wireguard-apple`, WireGuardKit)

- `WireGuardAdapter.start` builds a `PacketTunnelSettingsGenerator` from
  `resolvePeers`. That calls `DNSResolver.resolveSync`, which uses
  `getaddrinfo` with `AI_ALL` and prefers IPv4. Any failure throws
  `dnsResolution`, and the tunnel does not start [I1][I2].
- The generator stores the **resolved** endpoints. Every later config push
  (`uapiConfiguration`, `endpointUapiConfiguration`) passes the stored
  endpoints through `reresolveEndpoint` → `Endpoint.withReresolvedIP`.
  - On iOS that calls `getaddrinfo` with `ai_flags = 0` on the *IP string*,
    "so that we actually resolve this using DNS64".
  - On macOS `withReresolvedIP` does nothing.
  - On neither platform is the hostname looked up again [I2][I3].
- `didReceivePathUpdate`, driven by `NWPathMonitor`, behaves as follows:
  - macOS calls `wgBumpSockets` and nothing else [I1].
  - iOS, while a network path is available: send the endpoints again (as
    above), disable roaming, bump the sockets.
  - iOS, when no path is available: turn the backend off
    (`temporaryShutdown`). When the path returns, restart the backend from
    the **same** settings generator, so with the same IPs [I1].
- `update(tunnelConfiguration:)` does resolve again. The app calls it only when
  it pushes an edited config to a running tunnel [I1].
- `wgBumpSockets` retries `BindUpdate` up to 10 times. It then sends keepalives
  with the current keypair [I4].
- The on-demand options map to `NEOnDemandRuleConnect` and `Disconnect` rules.
  The rules cover any interface, Wi-Fi (optionally by SSID), and cellular (iOS)
  or Ethernet (macOS) [I5].
  - On-demand only starts a tunnel that is not running. The extension does not
    stop itself when handshakes fail, so a dead Bridge does not trigger it.
  - If the user disconnects by hand, on-demand starts the tunnel again. That is
    a fresh `start`, so the name is resolved again.
- The config parser rejects a repeated `Endpoint` key. It also rejects two peers
  with the same public key [I6].
- Mailing list:
  - A 2022 question asked whether the macOS app can resolve again on wake, on a
    network change or on an interval. No maintainer answered [M2].
  - A 2022 feature request asked for configurable re-resolution in the apps. It
    notes that the `reresolve-dns` script cannot run on Android [M3].
- The repo's last commit is from February 2023. Shipped App Store builds were
  not compared with it.

### Windows (`wireguard-windows`, WireGuardNT)

- The tunnel service (`tunnel/service.go`) logs "Resolving DNS names" and calls
  `config.ResolveEndpoints()` once, before it creates the adapter. A failure
  sets `services.ErrorDNSLookup`, and the service exits [W1].
- `resolveHostname` tries 10 times and sleeps 4 s after `WSATRY_AGAIN`. At boot
  it tries 30 times and also retries `WSAHOST_NOT_FOUND`.
  `resolveHostnameOnce` returns the first IPv4 address, else the first IPv6
  [W2].
- No code path resolves the name again while the tunnel runs. The service is
  installed as `StartAutomatic`, so it resolves again at every boot [W5].
- From `docs/netquirk.md` [W3]:
  - The allowed IPs of all peers are de-duplicated and added as routes.
  - One peer with a `/0` turns on a firewall kill switch. Using `0/1` plus
    `128/1` avoids it.
  - WireGuardNT "monitors the routing table … and always replies to the sender
    in that way".
- The Windows app has no on-demand option and no reconnect on network change.

### Linux (`wg-quick` + kernel module)

- wg-quick passes the config to `wg setconf` or `addconf`. `parse_endpoint` in
  `src/config.c` calls `getaddrinfo`. Non-permanent errors are retried 15 times
  by default; `WG_ENDPOINT_RESOLUTION_RETRIES` changes that (an integer or
  `infinity`) [L1][L2]. The kernel stores only the resulting address.
- `contrib/reresolve-dns/reresolve-dns.sh` is not installed by wg-quick. It
  reads the config file. For each peer whose latest handshake is more than
  135 s old, it runs `wg set <if> peer <key> endpoint <Endpoint>`, which
  resolves the name again. Its README says to run it from cron "every thirty
  seconds or so" [L3].
- wg(8): the endpoint "will be updated automatically to the most recent source
  IP address and port of correctly authenticated packets from the peer" [L4].

### Engine timers (all platforms)

From `wireguard-go/device/constants.go` and `timers.go` [G2][G3], which match
the timers section of the protocol paper [P2]:

- `RekeyTimeout` is 5 s, `KeepaliveTimeout` 10 s, `RekeyAttemptTime` 90 s and
  `RejectAfterTime` 180 s.
- `expiredNewHandshake` logs "Retrying handshake because we stopped hearing
  back after 15 seconds". It fires when data was sent and nothing came back.
- `expiredRetransmitHandshake` retries every 5 s. After `MaxTimerHandshakes`
  (18) it logs "giving up", flushes queued packets, and schedules key zeroing
  at `RejectAfterTime * 3` (540 s). A new outgoing packet starts the cycle
  again.
- Before each retry, `markEndpointSrcForClearing` clears the cached source
  address. The destination address is never changed.

A Device therefore notices a dead Bridge about 15–20 s after it tries to send
traffic. It keeps retrying the **same IP** in 90 s bursts and never tells the
user or the OS.

## Implications for the three failover strategies

### 1. Short-TTL hostname repointed by the Coordinator

- **No stock app fails over on its own.** All five platforms keep the IP they
  resolved when the tunnel came up. None resolves again on handshake failure,
  on a network change or when the TTL expires
  [A1][A2][I1][I3][W1][L1].
- Recovery needs a **tunnel restart**. Any of these causes one:
  - the user toggles the tunnel off and on (on Android, only after the
    2-minute cache expires [A2]);
  - the device reboots (Windows service [W5], Android always-on [A4]);
  - on iOS/macOS, on-demand starts the tunnel again after a manual disconnect
    [I5].

  No new config is needed, but the user has to act, and has to be told to.
- Linux recovers by itself if the Device runs `reresolve-dns.sh` from a timer
  [L3]. That is outside the stock app, and it acts only after 135 s without a
  handshake.
- A short TTL still speeds up the restart path. On Android the 2-minute cache
  makes any TTL below about 2 minutes useless [A2].
- This point is not a WireGuard fact: the name is resolved over the Device's
  physical network before the tunnel exists. The query is visible to the local
  resolver, and blocking or poisoning the name makes every restart fail.

### 2. Floating or anycast IP

- **This matches what the apps do.** Every app sends to one fixed IP forever.
  After 15 s of silence it retries handshakes against that IP every 5 s
  [G2][G3]. If the IP starts routing to a live Bridge, the next handshake
  reaches that Bridge.
- **It works only if the new Bridge can complete the handshake.** That Bridge
  must hold the private key matching the peer `PublicKey` in the Device config,
  and must know the Device's public key. With per-Bridge keys, the moved IP
  reaches a Bridge that rejects the handshake. This strategy therefore depends
  on strategy 3.
- With anycast, a route change mid-session sends packets to a Bridge that lacks
  the session keys. Data is dropped until the client's 15 s timer forces a new
  handshake [G3]. Expect about 15–20 s of outage per path change.
- Inbound roaming is disabled on Android and iOS [A3][I1][G1]. A reply from a
  *different* source IP would not move the client, so replies must come from
  the same floating or anycast IP. On macOS, Windows and Linux roaming is on,
  and the client would follow an authenticated reply from a new address
  [L4][P1]. Still, no Bridge can send such a reply unless the Device reached it
  first.

### 3. Bridges in a group sharing one WireGuard identity

- Strategy 2 needs it. Strategy 1 needs it too, unless every repoint comes with
  a new config: with per-Bridge keys, the `PublicKey` in the config would also
  have to change.
- A client config cannot say "same key, several endpoints". Apple rejects two
  peers with the same public key and rejects repeated `Endpoint` keys [I6].
  Android and Windows silently keep the last `Endpoint` [A7][W6].
- Two peers with *different* keys and the same `0.0.0.0/0` do not fail over
  either. The later peer takes the exact prefix, and the first peer gets no
  traffic [G4]. Giving each peer one `/1` half splits the traffic between them;
  it does not fail over.
- A shared identity therefore gives failover only when it is paired with a
  destination IP that moves (strategy 2), or with a restart after a DNS change
  (strategy 1).

### Which strategy the facts favour

A floating or anycast IP **plus** a shared WireGuard identity per Bridge group
is the only design in which a stock app recovers with no user action on all
five platforms. A short-TTL hostname with a shared identity can serve as the
fallback: recovery is "toggle the tunnel" (or reboot), and it costs nothing
extra. The Device config can carry a hostname that normally resolves to the
floating IP.

### Domestic bypass on the client (AllowedIPs size)

No WireGuard project code limits the number of AllowedIPs entries. Each entry
becomes an OS route on Android [A6], Windows [W3] and Apple platforms. The only
concrete ceiling found is a report on Apple's developer forum of a Network
Extension crashing for memory at 2048 IPv6 routes [X1]; it is not a WireGuard
source. Test a route list of thousands of prefixes (such as the complement of
Iran's ranges) on iOS before relying on it.

## Caveats

- The code was read at the head of each repo (commits below), not at the exact
  versions in the app stores. The wireguard-apple head is from February 2023.
- WireGuardNT's timers were not read directly. They are assumed to match the
  protocol paper and wireguard-go [P2].
- Nothing was tested on a device. Every finding comes from source and docs.
- The Apple route-count figure is a report on Apple's forum, not a WireGuard
  statement. No AllowedIPs limit was found for Android or Windows.
- The Android and Windows parsers do not check for two peers with the same
  public key, and what the engine then does was not traced.

## Sources

Commits read (GitHub mirrors of git.zx2c4.com):
wireguard-android `e7b3a3c`, wireguard-apple `2fec12a`, wireguard-windows
`6ece77b`, wireguard-go `2631ce9`, wireguard-tools `a998407`.

- [A1] GoBackend.setStateInternal, DNS retry loop: https://github.com/WireGuard/wireguard-android/blob/e7b3a3c118836e112620b1302a8ba1873ad4daac/tunnel/src/main/java/com/wireguard/android/backend/GoBackend.java#L275-L290
- [A2] InetEndpoint.getResolved: https://github.com/WireGuard/wireguard-android/blob/e7b3a3c118836e112620b1302a8ba1873ad4daac/tunnel/src/main/java/com/wireguard/config/InetEndpoint.java#L90-L113
- [A3] libwg-go wgTurnOn: https://github.com/WireGuard/wireguard-android/blob/e7b3a3c118836e112620b1302a8ba1873ad4daac/tunnel/tools/libwg-go/api-android.go#L99
- [A4] GoBackend.VpnService.onStartCommand: https://github.com/WireGuard/wireguard-android/blob/e7b3a3c118836e112620b1302a8ba1873ad4daac/tunnel/src/main/java/com/wireguard/android/backend/GoBackend.java#L414-L421
- [A5] Application always-on callback: https://github.com/WireGuard/wireguard-android/blob/e7b3a3c118836e112620b1302a8ba1873ad4daac/ui/src/main/java/com/wireguard/android/Application.kt#L80
- [A6] GoBackend routes and kill switch: https://github.com/WireGuard/wireguard-android/blob/e7b3a3c118836e112620b1302a8ba1873ad4daac/tunnel/src/main/java/com/wireguard/android/backend/GoBackend.java#L314-L328
- [A7] Peer.Builder.parseEndpoint: https://github.com/WireGuard/wireguard-android/blob/e7b3a3c118836e112620b1302a8ba1873ad4daac/tunnel/src/main/java/com/wireguard/config/Peer.java#L248-L254
- [I1] WireGuardAdapter (start, update, resolvePeers, didReceivePathUpdate): https://github.com/WireGuard/wireguard-apple/blob/2fec12a6e1f6e3460b6ee483aa00ad29cddadab1/Sources/WireGuardKit/WireGuardAdapter.swift#L177-L460
- [I2] DNSResolver.resolveSync: https://github.com/WireGuard/wireguard-apple/blob/2fec12a6e1f6e3460b6ee483aa00ad29cddadab1/Sources/WireGuardKit/DNSResolver.swift#L14-L90
- [I3] Endpoint.withReresolvedIP and PacketTunnelSettingsGenerator.endpointUapiConfiguration: https://github.com/WireGuard/wireguard-apple/blob/2fec12a6e1f6e3460b6ee483aa00ad29cddadab1/Sources/WireGuardKit/DNSResolver.swift#L94-L130 and https://github.com/WireGuard/wireguard-apple/blob/2fec12a6e1f6e3460b6ee483aa00ad29cddadab1/Sources/WireGuardKit/PacketTunnelSettingsGenerator.swift#L24-L40
- [I4] wgBumpSockets, wgDisableSomeRoamingForBrokenMobileSemantics: https://github.com/WireGuard/wireguard-apple/blob/2fec12a6e1f6e3460b6ee483aa00ad29cddadab1/Sources/WireGuardKitGo/api-apple.go#L177-L203
- [I5] ActivateOnDemandOption.apply: https://github.com/WireGuard/wireguard-apple/blob/2fec12a6e1f6e3460b6ee483aa00ad29cddadab1/Sources/WireGuardApp/Tunnel/ActivateOnDemandOption.swift#L14-L44
- [I6] TunnelConfiguration(fromWgQuickConfig:): https://github.com/WireGuard/wireguard-apple/blob/2fec12a6e1f6e3460b6ee483aa00ad29cddadab1/Sources/Shared/Model/TunnelConfiguration+WgQuickConfig.swift#L64-L116
- [W1] Tunnel service run, ResolveEndpoints call: https://github.com/WireGuard/wireguard-windows/blob/6ece77bc487c8aa697e3c092197621c4f3e5ccb8/tunnel/service.go#L147-L152
- [W2] resolveHostname, resolveHostnameOnce, ResolveEndpoints: https://github.com/WireGuard/wireguard-windows/blob/6ece77bc487c8aa697e3c092197621c4f3e5ccb8/conf/dnsresolver_windows.go#L20-L97
- [W3] docs/netquirk.md: https://github.com/WireGuard/wireguard-windows/blob/6ece77bc487c8aa697e3c092197621c4f3e5ccb8/docs/netquirk.md
- [W4] ui/confview.go, latest handshake: https://github.com/WireGuard/wireguard-windows/blob/6ece77bc487c8aa697e3c092197621c4f3e5ccb8/ui/confview.go#L486-L487
- [W5] manager InstallTunnel, StartAutomatic: https://github.com/WireGuard/wireguard-windows/blob/6ece77bc487c8aa697e3c092197621c4f3e5ccb8/manager/install.go#L167-L172
- [W6] conf parser, endpoint key: https://github.com/WireGuard/wireguard-windows/blob/6ece77bc487c8aa697e3c092197621c4f3e5ccb8/conf/parser.go#L341-L346
- [G1] wireguard-go DisableSomeRoamingForBrokenMobileSemantics, SetEndpointFromPacket: https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/mobilequirks.go and https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/peer.go#L279-L287
- [G2] wireguard-go timer constants: https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/constants.go#L15-L23
- [G3] wireguard-go expiredRetransmitHandshake, expiredNewHandshake: https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/timers.go#L79-L124
- [G4] wireguard-go allowedips insert (an exact prefix is reassigned): https://github.com/WireGuard/wireguard-go/blob/2631ce99a06f27120d581611cf125d68bc6aa565/device/allowedips.go#L101-L122
- [L1] wireguard-tools parse_endpoint: https://github.com/WireGuard/wireguard-tools/blob/a998407747005ea7e4e0258d96f105c97241e1d3/src/config.c#L177-L278
- [L2] wg(8), WG_ENDPOINT_RESOLUTION_RETRIES: https://github.com/WireGuard/wireguard-tools/blob/a998407747005ea7e4e0258d96f105c97241e1d3/src/man/wg.8#L243-L244
- [L3] reresolve-dns.sh and README: https://github.com/WireGuard/wireguard-tools/blob/a998407747005ea7e4e0258d96f105c97241e1d3/contrib/reresolve-dns/reresolve-dns.sh and https://git.zx2c4.com/wireguard-tools/plain/contrib/reresolve-dns/README
- [L4] wg(8), Endpoint: https://github.com/WireGuard/wireguard-tools/blob/a998407747005ea7e4e0258d96f105c97241e1d3/src/man/wg.8#L165-L168
- [P1] wireguard.com, Built-in Roaming and Cryptokey Routing: https://www.wireguard.com/
- [P2] WireGuard whitepaper, section 6 (timers): https://www.wireguard.com/papers/wireguard.pdf
- [M1] "WireGuard Android: Cached resolved host" (2018): https://lists.zx2c4.com/pipermail/wireguard/2018-October/003508.html
- [M2] "[wireguard-apple] Reresolving DNS on device wake/network change/interval" (2022): https://lists.zx2c4.com/pipermail/wireguard/2022-April/007541.html
- [M3] "Feature Request: Configurable Re-Resolving of DNS Endpoints" (2022): https://lists.zx2c4.com/pipermail/wireguard/2022-April/007599.html
- [X1] Apple Developer Forums, "Network extension crash with 2048 IPv6 split tunnel routes": https://developer.apple.com/forums/thread/723156
