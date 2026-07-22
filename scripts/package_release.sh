#!/usr/bin/env bash
# Package CPA plugin store assets:
#   cpa-grok-panel_<semver>_linux_amd64.zip
#   cpa-grok-panel_<semver>_linux_arm64.zip     (aarch64-linux-gnu-gcc)
#   cpa-grok-panel_<semver>_windows_amd64.zip  (x86_64-w64-mingw32-gcc)
#   cpa-grok-panel_<semver>_windows_arm64.zip  (aarch64-w64-mingw32-gcc / llvm-mingw)
# Zip root contains cpa-grok-panel.so (linux) or cpa-grok-panel.dll (windows).
# Usage: scripts/package_release.sh [version]
# Example: scripts/package_release.sh 0.5.4
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
export PATH="/usr/local/go/bin:/opt/llvm-mingw/bin:${PATH}"
mkdir -p dist

build_one() {
  local goos="$1"
  local goarch="$2"
  local cc="${3:-}"
  local ext
  if [[ "$goos" == "windows" ]]; then
    ext="dll"
  else
    ext="so"
  fi
  local out="dist/cpa-grok-panel_${goos}_${goarch}.${ext}"
  local zip_path="dist/cpa-grok-panel_${VERSION}_${goos}_${goarch}.zip"
  local arcname="cpa-grok-panel.${ext}"
  echo "building ${arcname} (${goos}/${goarch} c-shared)..."
  if [[ -n "$cc" ]]; then
    CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" CC="$cc" \
      go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o "$out" .
  else
    CGO_ENABLED=1 GOOS="$goos" GOARCH="$goarch" \
      go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o "$out" .
  fi
  # drop generated headers
  rm -f "${out%.${ext}}.h" "dist/cpa-grok-panel_${goos}_${goarch}.h" 2>/dev/null || true
  if [[ "$goos" == "linux" && "$goarch" == "amd64" ]]; then
    cp -f "$out" dist/cpa-grok-panel.so
  fi
  python3 - <<PY
import pathlib, zipfile
out = pathlib.Path("$out")
zip_path = pathlib.Path("$zip_path")
with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    zf.write(out, arcname="$arcname")
print(zip_path)
PY
}

# --- linux ---
build_one linux amd64

ARM_CC=""
if command -v aarch64-linux-gnu-gcc >/dev/null 2>&1; then
  ARM_CC=aarch64-linux-gnu-gcc
elif command -v aarch64-unknown-linux-gnu-gcc >/dev/null 2>&1; then
  ARM_CC=aarch64-unknown-linux-gnu-gcc
fi
if [[ -n "$ARM_CC" ]]; then
  build_one linux arm64 "$ARM_CC"
else
  echo "WARN: no aarch64 linux cross-gcc — skip linux/arm64" >&2
fi

# --- windows ---
WIN_AMD64_CC=""
if command -v x86_64-w64-mingw32-gcc >/dev/null 2>&1; then
  WIN_AMD64_CC=x86_64-w64-mingw32-gcc
fi
if [[ -n "$WIN_AMD64_CC" ]]; then
  build_one windows amd64 "$WIN_AMD64_CC"
else
  echo "WARN: no x86_64-w64-mingw32-gcc — skip windows/amd64" >&2
fi

WIN_ARM64_CC=""
if command -v aarch64-w64-mingw32-gcc >/dev/null 2>&1; then
  WIN_ARM64_CC=aarch64-w64-mingw32-gcc
elif command -v aarch64-w64-mingw32-clang >/dev/null 2>&1; then
  WIN_ARM64_CC=aarch64-w64-mingw32-clang
fi
if [[ -n "$WIN_ARM64_CC" ]]; then
  build_one windows arm64 "$WIN_ARM64_CC"
else
  echo "WARN: no aarch64 windows cross-gcc — skip windows/arm64" >&2
fi

python3 - <<PY
import hashlib, pathlib, re
dist = pathlib.Path("dist")
version = "$VERSION"
zips = sorted(dist.glob(f"cpa-grok-panel_{version}_*.zip"))
if not zips:
    raise SystemExit("no release zips produced")
lines = []
for z in zips:
    h = hashlib.sha256(z.read_bytes()).hexdigest()
    lines.append(f"{h}  {z.name}")
# optional legacy so hash
so = dist / "cpa-grok-panel.so"
if so.exists():
    lines.append(f"{hashlib.sha256(so.read_bytes()).hexdigest()}  {so.name}")
(dist / "checksums.txt").write_text("\n".join(lines) + "\n")
print("checksums:")
print((dist / "checksums.txt").read_text())
print("done. upload with:")
print(f"  gh release upload v{version} dist/cpa-grok-panel_{version}_*.zip dist/checksums.txt --clobber")
PY
