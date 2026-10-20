#!/usr/bin/env bash
# build-release.sh VERSION OUTDIR builds one release: the Agent, the
# Coordinator and the pinned amneziawg-go for linux/amd64, plus the pinned
# Prometheus, Alertmanager and Grafana tarballs, and a SHA256SUMS over them.
# The panel must already be built (web: npm run build).
#
# WAYS_DOWNLOAD_CACHE, if set, keeps the upstream tarballs between builds.
set -euo pipefail
version="$1" out="$2"
root="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=../deploy/versions.env
. "$root/deploy/versions.env"
cache="${WAYS_DOWNLOAD_CACHE:-$(mktemp -d)}"
mkdir -p "$out" "$cache"

ls "$root"/internal/panel/dist/app/index.html >/dev/null 2>&1 \
  || { echo "build the panel first: (cd web && npm ci && npm run build)" >&2; exit 1; }

export CGO_ENABLED=0 GOOS=linux GOARCH=amd64
(cd "$root" && go build -trimpath -ldflags "-X main.version=$version" -o "$out/ways-agent" ./cmd/agent)
(cd "$root" && go build -trimpath -ldflags "-X main.version=$version" -o "$out/ways-coordinator" ./cmd/coordinator)
(cd "$root" && go build -trimpath -o "$out/amneziawg-go" github.com/amnezia-vpn/amneziawg-go)

fetch() { # fetch URL SHA256
  local file="$cache/${1##*/}"
  if ! echo "$2  $file" | sha256sum -c --status - 2>/dev/null; then
    curl -fsSL -o "$file" "$1"
    echo "$2  $file" | sha256sum -c --quiet -
  fi
  cp "$file" "$out/"
}
fetch "https://github.com/prometheus/prometheus/releases/download/v$PROMETHEUS/prometheus-$PROMETHEUS.linux-amd64.tar.gz" "$PROMETHEUS_SHA256"
fetch "https://github.com/prometheus/alertmanager/releases/download/v$ALERTMANAGER/alertmanager-$ALERTMANAGER.linux-amd64.tar.gz" "$ALERTMANAGER_SHA256"
fetch "https://dl.grafana.com/oss/release/grafana-$GRAFANA.linux-amd64.tar.gz" "$GRAFANA_SHA256"

cp "$root/deploy/versions.env" "$out/versions.env"
echo "$version" > "$out/VERSION"
(cd "$out" && sha256sum -- * | grep -v ' SHA256SUMS$' > SHA256SUMS)
echo "built $version in $out"
