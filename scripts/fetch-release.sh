#!/usr/bin/env bash
# fetch-release.sh VERSION downloads a release from GitHub to the laptop,
# into ~/.darz/releases/VERSION, and checks its SHA256SUMS. The playbooks
# push it from there, so no Node ever downloads from abroad.
# Run it with a VPN if you're in Iran: Grafana alone is ~450 MB.
set -euo pipefail
version="$1"
dir="${DARZ_HOME:-$HOME/.darz}/releases/$version"
mkdir -p "$dir"
gh release download "$version" --repo alakiani/darz --dir "$dir" --skip-existing
cd "$dir"
if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi
echo "release $version is in $dir"
