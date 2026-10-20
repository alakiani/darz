# Iranian CIDR list: sources, disagreement and refresh cadence

Research for issue #5. Question: where can the Coordinator get an authoritative,
maintained set of Iranian IP ranges to feed the domestic **Rule** (destination
CIDRs that must leave through an **Exit** Node tagged domestic), and how often
does that set change?

All measurements were taken on 2026-10-14 against the live sources listed
below. The data files were downloaded to a scratch directory and parsed with
`python3 -I`; none are committed.

## Recommendation (short)

- **Source:** the RIPE NCC `delegated-ripencc-extended-latest` file, rows with
  country `IR` and status `allocated` or `assigned`, both `ipv4` and `ipv6`.
  It is the registry of record, published daily with an MD5 file, and archived
  per day so the Coordinator can roll back.
- **Cross-check:** RIPEstat `country-resource-list` for `IR`. It is derived
  from the same RIR statistics and returned an identical set on the test day,
  so it doubles as an independent second fetch for the sanity check.
- **Cadence:** fetch once a day, push to Bridges only when the set changes.
  Measured churn is about one row per week.
- **Overrides:** an admin-maintained add/remove CIDR list on top of the feed
  for the known registry-vs-reality gaps (section "Gotchas").
- Do **not** use GeoIP-derived lists (DB-IP, MaxMind, the v2ray `geoip` files)
  as the primary source: licence strings attached, slower cadence, and they
  disagree with the registry by 2 to 4 percent of the address space.

## Source comparison

| Source | How it is built | Licence / terms | Update frequency | Format | IPv6 |
|---|---|---|---|---|---|
| RIPE NCC delegated-extended | Registry records; `cc` is "the country in which the resource holder is legally based" [1] | General RIPE NCC Terms of Service for public services [2]; no data-specific licence found | "produced daily" [1]; file timestamped ~22:12 Amsterdam time [3] | Pipe-separated text, 18 MB, `.md5` alongside, dated `.bz2` archives per year [3][4] | Yes (573 IR rows) |
| RIPEstat `country-resource-list` | "derived from the RIR Statistics files maintained by the various RIRs" [5] | RIPEstat Service Terms; no request limit, 8 concurrent requests per IP, register above 1,000/day, `sourceapp` id requested [6] | Not stated; `query_time` in response (was the previous midnight) [5] | JSON, `v4_format=prefix` gives CIDRs [5] | Yes |
| NRO combined delegated stats | Same format, all five RIRs merged [7] | As RIPE NCC | Daily [7] | Pipe-separated text | Yes |
| bgp.tools `table.jsonl` | Live BGP table seen by bgp.tools collectors: `CIDR`, origin `ASN`, `Hits` (visibility) [8] | No licence stated; must send an identifying User-Agent with contact; refresh no more than every 30 min [8] | ~30 min [8] | JSON lines, 77 MB, 1.5 M rows | Yes |
| CAIDA RouteViews prefix2as | RouteViews BGP data, prefix to origin AS [9] | CAIDA Acceptable Use Agreement, citation required [9] | Daily [9] | TSV prefix, length, AS | Yes |
| v2fly/geoip `text/ir.txt` | DB-IP "IP to Country Lite" MMDB plus private ranges [10][11] | Repo CC-BY-SA-4.0 [12]; DB-IP Lite CC BY 4.0 with attribution [13] | Monthly, cron `0 0 5 * *` [11]; DB-IP Lite "updated monthly" [13] | Text CIDR list | Yes |
| Loyalsoldier/geoip `text/ir.txt` | MaxMind GeoLite2 Country CSV [14] | Repo CC-BY-SA-4.0; GeoLite EULA needs a licence key and forces deletion of data older than 30 days after a new release [15] | Weekly, cron `0 0 * * 4` [16] | Text CIDR list | Yes |
| Chocolate4U/Iran-v2ray-rules `text/ir.txt` | GeoLite2 + IP2Location LITE + Iranian CDN ranges (ArvanCloud, Derak, IranServer, ParsPack) + "Messengers" list from an ito.gov.ir mirror + internal DNS IPs `10.202.10.x` [17][18][19] | GPL-3.0 repo [12]; inherits GeoLite/IP2Location terms | Daily build, cron `40 4 * * *`; GeoLite2 twice a week, IP2Location 1st and 15th [18][20] | Text CIDR list | Yes |
| ipdeny aggregated zones | Not documented [21]; measured identical to RIPE delegated IR (below) | Free redistribution allowed, linkback recommended [21] | Not stated; page showed same-day update [21] | Text CIDR list, separate v4/v6 files | Yes |

[herrbischoff/country-ip-blocks](https://github.com/herrbischoff/country-ip-blocks)
returned HTTP 404 for `ipv4/ir.cidr` on the test day and was dropped.

## Measured disagreement (2026-10-14)

Baseline: RIPE delegated-extended IR, 1,969 IPv4 rows = 1,972 CIDRs =
10,840,064 addresses; 573 IPv6 rows = 4,076 /32-equivalents; 858 IR ASNs.

| Source | v4 CIDRs | v4 addresses | % of its v4 inside RIPE IR | % of RIPE IR v4 it covers | v6 CIDRs | % of RIPE IR v6 it covers |
|---|---:|---:|---:|---:|---:|---:|
| RIPE delegated-extended | 1,972 | 10.840 M | 100.00 | 100.00 | 573 | 100.00 |
| RIPEstat country-resource-list | 1,972 | 10.840 M | 100.00 | 100.00 | 573 | 100.00 |
| ipdeny aggregated | 1,747 | 10.840 M | 100.00 | 100.00 | 572 | 100.00 |
| bgp.tools, origin ASN registered IR | 8,751 | 10.230 M | 98.13 | 92.61 | 357 | 15.63 |
| bgp.tools, any origin, inside RIPE IR | 8,469 | 10.135 M | 99.92 | 93.42 | 366 | 17.12 |
| v2fly/geoip (DB-IP Lite) | 2,024 | 10.998 M | 97.42 | 98.83 | 1,014 | 92.64 |
| Loyalsoldier/geoip (GeoLite2) | 2,073 | 10.962 M | 98.32 | 99.43 | 713 | 98.60 |
| Chocolate4U (GeoLite2+IP2L+CDN) | 2,212 | 11.032 M | 97.87 | 99.61 | 714 | 98.60 |

IPv4 Jaccard similarity by address space, against RIPE: RIPEstat 1.000,
ipdeny 1.000, GeoLite2 0.978, Chocolate4U 0.975, DB-IP 0.963, BGP by IR origin
ASN 0.910. GeoLite2 vs DB-IP: 0.983.

Why they differ:

- **Allocated vs announced.** 0.80 M RIPE-IR IPv4 addresses (7.4 %, 527
  ranges) are not originated by any IR-registered ASN in BGP. IPv6 is mostly
  allocated but unrouted: only ~16 % of IR IPv6 space is announced. A
  registry-based list is therefore a superset of what is reachable; that is
  harmless for a Rule (unrouted space carries no traffic).
- **Holder country vs usage country.** The registry `cc` is the holder's legal
  seat and "can therefore not be reliably used to map IP addresses to
  countries" [1]. Iranian ASNs announce 191,488 IPv4 addresses (52 ranges)
  that are registered to other countries, e.g. `5.160.0.0/16`, registered `AE`
  [3] and originated by AS42337 Respina Networks [22]; `5.10.248.0/23`
  registered `AZ`, originated by AS212607.
- **Iranian-registered space used abroad.** 268 IPv4 announcements (89,088
  addresses) inside IR-registered space are originated by 101 ASNs that are
  not IR-registered, e.g. `62.60.148.0/24` by AS210644 AEZA Group and
  `62.60.152.0/24` by AS203273 NetCrafters, plus AS3561 CenturyLink/Savvis
  and AS216050 Lietparkas [22].
- **GeoIP vendors** blend registry data with their own geolocation. They add
  184k to 284k IPv4 addresses that are not IR-registered (largely the
  `5.160.0.0/16`-style cases above) and drop 42k to 127k that are.

## Gotchas

1. **Mobile operators are clean.** Every IPv4 prefix originated by MCI
   (AS197207, 802 prefixes), Irancell (AS44244, 404), RighTel (AS57218, 76),
   TCI (AS58224, 1,192) and DCI (AS12880, 71) falls inside RIPE IR space; all
   five also announce IPv6 [8][22]. No special handling needed.
2. **Iranian CDN with foreign PoPs.** ArvanCloud publishes its edge ranges at
   `https://www.arvancloud.ir/en/ips.txt` (used by Chocolate4U [19]). Of its
   2,300 IPv4 addresses, 1,268 are IR-registered; `185.215.232.0/22` is
   registered `AE` [3] and originated by AS208006 "ARVANCLOUD GLOBAL
   TECHNOLOGIES L.L.C" [22]. A Rule built only from the registry will send
   that range abroad. That is usually correct (it is a foreign PoP) but worth
   knowing when debugging an Iranian site behind ArvanCloud.
3. **Iranian sites behind foreign CDNs** (Cloudflare and similar) cannot be
   matched by an IP-level Rule at all; they will use the abroad Exit. This is
   inherent to the IP-only policy (domain rules are out of scope).
4. **IR-registered ranges that live abroad** (the 89k addresses above, AEZA,
   NetCrafters, Savvis, ...): routing them to the domestic Exit sends the
   traffic into Iran and back out through the national gateway, where it may
   be filtered. Small (0.8 % of IR v4) but it includes hosting ranges where
   foreign VPS live, so it can surprise users.
5. **Non-IR-registered ranges that are Iranian in practice** (Respina's
   `5.160.0.0/16`, AE-registered): the registry list misses them, so that
   traffic goes abroad and returns into Iran, with higher latency and the
   chance that the Iranian site geo-blocks foreign visitors.
6. **Iranian internal addresses.** Chocolate4U adds `10.202.10.10`,
   `10.202.10.11`, `10.202.10.102`, `10.202.10.202`, `172.29.0.100`,
   `172.29.2.100` to `ir` [17]: RFC 1918 addresses reachable only inside
   Iranian networks (domestic DNS services). They are not in any registry
   list; add them through the override list if domestic DNS must work.
7. **How circumvention projects handle this:** they do not use the registry
   directly. v2fly uses DB-IP Lite [10]; Loyalsoldier uses GeoLite2 [14];
   Chocolate4U, the one Iran-focused project, merges GeoLite2 and IP2Location
   and then explicitly adds Iranian CDN ranges, the government "Messengers"
   list and the internal DNS IPs to `ir` [17][19]. That is the same pattern
   recommended here: a base feed plus curated additions.
8. **Range rows are not CIDRs.** IPv4 rows give a start address and a host
   count that need not be a power of two (1,969 rows became 1,972 CIDRs).
   Go: walk `netip.Addr` ranges into prefixes; IPv6 rows already give a prefix
   length [1].
9. **Licence wording.** RIPE NCC's general Terms forbid re-distributing data
   from the website "unless their use of the service permits it" [2]. Internal
   use by the Coordinator to configure our own Bridges is very likely fine, but
   no statistics-specific licence was found; flag it if the list is ever
   published.

## Fetch command

```sh
B=https://ftp.ripe.net/pub/stats/ripencc
curl -fsS -o delegated.txt     "$B/delegated-ripencc-extended-latest"
curl -fsS -o delegated.txt.md5 "$B/delegated-ripencc-extended-latest.md5"
# md5 file format: "MD5 (delegated-ripencc-extended-latest) = <hex>"
awk -F'|' '$2=="IR" && ($3=="ipv4"||$3=="ipv6") && ($7=="allocated"||$7=="assigned") {print $3, $4, $5}' delegated.txt
# ipv4: <start> <address count>  -> convert range to CIDRs
# ipv6: <prefix> <prefix length>
```

Cross-check fetch (same data, already CIDR, JSON):

```sh
curl -fsS "https://stat.ripe.net/data/country-resource-list/data.json?resource=IR&v4_format=prefix&sourceapp=ways-coordinator"
# .data.resources.ipv4[], .data.resources.ipv6[], .data.query_time
```

Rollback / history: `$B/<YYYY>/delegated-ripencc-extended-<YYYYMMDD>.bz2` [4].

Optional BGP augmentation (later, if gotchas 4 and 5 bite): take bgp.tools
`table.jsonl` [8] (or CAIDA prefix2as [9]), add prefixes whose origin ASN is
IR-registered, subtract prefixes inside IR space whose origin is not. bgp.tools
requires a User-Agent naming the operator and a contact address [8].

## Refresh cadence

Measured churn of IR `allocated`/`assigned` rows, RIPE archives:

| Snapshot | v4 rows | v4 addresses | v6 rows | Rows changed vs 2026-10-13 |
|---|---:|---:|---:|---:|
| 2025-10-14 | 1,884 | 10,798,848 | 564 | +128 / -34 |
| 2026-04-14 | 1,920 | 10,820,352 | 572 | +69 / -19 |
| 2026-09-14 | 1,957 | 10,838,272 | 571 | +17 / -3 |
| 2026-10-01 | 1,968 | 10,839,808 | 572 | +2 / 0 |
| 2026-10-06 .. 10-11 | 1,969 | 10,840,064 | 572 | +1 / 0 |
| 2026-10-13 | 1,969 | 10,840,064 | 573 | 0 |

Over a year the IPv4 address total moved +0.38 %; day to day it is usually 0
or 1 row. Recommendation:

- Fetch daily, a few hours after the file is cut (the archive files are
  timestamped ~22:12 Amsterdam time [4]), e.g. 03:00 UTC.
- Compute the CIDR set, diff against the last-good set, push to Bridges only on
  a change. A missed day costs nothing; a week of staleness is tolerable.
- Alert if the newest accepted file is more than 3 days old.

## Sanity check for a broken update

Reject the new set, keep serving last-good, and alert if any of these fail:

1. Both downloads succeed with HTTP 200, and the MD5 of `delegated.txt`
   matches the `.md5` file.
2. Header line `2|ripencc|<serial>|<records>|<start>|<enddate>|<utcoffset>`
   [1]: `<records>` equals the number of data lines (260,968 on the test day),
   `<enddate>` is not older than the last-good one, and `<serial>` grew.
3. Size bounds against last-good: IPv4 address total within ±2 % (annual drift
   is 0.4 %), IPv4 row count and IPv6 row count within ±5 %, and absolute
   floors of 10 M IPv4 addresses, 1,800 IPv4 rows and 500 IPv6 rows.
4. No CIDR in the set overlaps RFC 1918, loopback, multicast or `0.0.0.0/8`
   (catches a parser bug), and no CIDR is shorter than /8 (v4) or /16 (v6).
5. The RIPEstat cross-check agrees: IPv4 Jaccard by address space ≥ 0.99
   (measured 1.000). If RIPEstat is down, skip this check rather than fail.

A deliberate override for a check (for example a large real reallocation)
should be an admin action, not automatic.

## Sources

1. RIR Statistics Exchange Format: https://ftp.ripe.net/pub/stats/ripencc/RIR-Statistics-Exchange-Format.txt
2. RIPE NCC Terms of Service: https://www.ripe.net/about-us/legal/terms-of-service
3. RIPE NCC delegated-extended latest (fetched 2026-10-14): https://ftp.ripe.net/pub/stats/ripencc/delegated-ripencc-extended-latest
4. RIPE NCC stats directory and archives: https://ftp.ripe.net/pub/stats/ripencc/ , https://ftp.ripe.net/pub/stats/ripencc/2026/ , README https://ftp.ripe.net/pub/stats/ripencc/_README-ripe-stats.txt
5. RIPEstat country-resource-list: https://stat.ripe.net/docs/data-api/api-endpoints/country-resource-list
6. RIPEstat Data API usage: https://stat.ripe.net/docs/data-api/ripestat-data-api
7. NRO RIR statistics: https://www.nro.net/about/rirs/statistics/
8. bgp.tools API and table exports: https://bgp.tools/kb/api , data https://bgp.tools/table.jsonl
9. CAIDA RouteViews prefix2as: https://www.caida.org/catalog/datasets/routeviews-prefix2as/
10. v2fly/geoip config: https://github.com/v2fly/geoip/blob/master/config.json
11. v2fly/geoip workflow: https://github.com/v2fly/geoip/blob/master/.github/workflows/build.yml
12. Repo licences via GitHub API: https://github.com/v2fly/geoip , https://github.com/Loyalsoldier/geoip , https://github.com/Chocolate4U/Iran-v2ray-rules
13. DB-IP IP to Country Lite: https://db-ip.com/db/download/ip-to-country-lite
14. Loyalsoldier/geoip config: https://github.com/Loyalsoldier/geoip/blob/master/config.json
15. MaxMind GeoLite2: https://dev.maxmind.com/geoip/geolite2-free-geolocation-data/
16. Loyalsoldier/geoip workflow: https://github.com/Loyalsoldier/geoip/blob/master/.github/workflows/build.yml
17. Chocolate4U config: https://github.com/Chocolate4U/Iran-v2ray-rules/blob/main/config.json
18. Chocolate4U release workflow: https://github.com/Chocolate4U/Iran-v2ray-rules/blob/main/.github/workflows/release.yml
19. Chocolate4U domestic CDN script: https://github.com/Chocolate4U/Iran-v2ray-rules/blob/main/scripts/generate-domestic-cdn-ips.sh
20. Chocolate4U GeoLite2 / IP2Location workflows: https://github.com/Chocolate4U/Iran-v2ray-rules/blob/main/.github/workflows/get-geolite2.yml , https://github.com/Chocolate4U/Iran-v2ray-rules/blob/main/.github/workflows/get-ip2location.yml
21. ipdeny: https://www.ipdeny.com/ipblocks/
22. RIPEstat as-overview (AS holder names), e.g. https://stat.ripe.net/data/as-overview/data.json?resource=AS42337
