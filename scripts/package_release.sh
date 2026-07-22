#!/usr/bin/env bash
# Package CPA plugin store assets for one or more linux arches:
#   cpa-grok-panel_<semver>_linux_amd64.zip
#   cpa-grok-panel_<semver>_linux_arm64.zip  (when aarch64 cross-gcc present)
# Zip root contains cpa-grok-panel.so only.
# Usage: scripts/package_release.sh [version]
# Example: scripts/package_release.sh 0.5.2
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
VERSION="${1:-}"
if [[ -z "$VERSION" ]]; then
  VERSION="$(python3 - <<'PY'
import re, pathlib
text=pathlib.Path('internal/cpaabi/types.go').read_text()
m=re.search(r'PluginVersion\s*=\s*"([^"]+)"', text)
print(m.group(1) if m else '')
PY
)"
fi
VERSION="${VERSION#v}"
if [[ -z "$VERSION" ]]; then
  echo "version required" >&2
  exit 1
fi
export PATH="/usr/local/go/bin:${PATH}"
mkdir -p dist

build_arch() {
  local goarch="$1"
  local cc="${2:-}"
  local out_so="dist/cpa-grok-panel_${goarch}.so"
  local zip_path="dist/cpa-grok-panel_${VERSION}_linux_${goarch}.zip"
  echo "building cpa-grok-panel.so (linux/${goarch} c-shared)..."
  if [[ -n "$cc" ]]; then
    CGO_ENABLED=1 GOOS=linux GOARCH="$goarch" CC="$cc" \
      go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o "$out_so" .
  else
    CGO_ENABLED=1 GOOS=linux GOARCH="$goarch" \
      go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o "$out_so" .
  fi
  # keep legacy name for amd64 intermediate too
  if [[ "$goarch" == "amd64" ]]; then
    cp -f "$out_so" dist/cpa-grok-panel.so
    # header from amd64 build
    if [[ -f dist/cpa-grok-panel_${goarch}.h ]]; then
      mv -f "dist/cpa-grok-panel_${goarch}.h" dist/cpa-grok-panel.h 2>/dev/null || true
    fi
  fi
  # go -buildmode=c-shared may emit .h next to .so
  local hdr="${out_so%.so}.h"
  if [[ -f "$hdr" && "$goarch" != "amd64" ]]; then
    rm -f "$hdr"
  fi
  python3 - <<PY
import pathlib, zipfile
so = pathlib.Path("$out_so")
zip_path = pathlib.Path("$zip_path")
with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    zf.write(so, arcname="cpa-grok-panel.so")
print(zip_path)
PY
}

build_arch amd64

ARM_CC=""
if command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; then
  ARM_CC=aarch64-linux-gnu-gcc
elif command -v aarch64-unknown-linux-gnu-gcc >/dev/null 2>&1; then
  ARM_CC=aarch64-unknown-linux-gnu-gcc
fi
if [[ -n "$ARM_CC" ]]; then
  build_arch arm64 "$ARM_CC"
else
  echo "WARN: no aarch64 cross-gcc — skip linux/arm64 package" >&2
fi

python3 - <<PY
import hashlib, pathlib, re
version = "$VERSION"
dist = pathlib.Path("dist")
zips = sorted(dist.glob(f"cpa-grok-panel_{version}_linux_*.zip"))
if not zips:
    raise SystemExit("no zip assets produced")
lines = []
for z in zips:
    lines.append(f"{hashlib.sha256(z.read_bytes()).hexdigest()}  {z.name}")
# also hash primary amd64 .so if present
so = dist / "cpa-grok-panel.so"
if so.exists():
    lines.append(f"{hashlib.sha256(so.read_bytes()).hexdigest()}  {so.name}")
pathlib.Path("dist/checksums.txt").write_text("\n".join(lines) + "\n")
print("checksums:")
print("\n".join(lines))
PY

echo "done. upload with:"
echo "  gh release upload v${VERSION} dist/cpa-grok-panel_${VERSION}_linux_*.zip dist/checksums.txt --clobber"
