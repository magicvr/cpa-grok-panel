#!/usr/bin/env bash
# Package CPA plugin store asset:
#   cpa-grok-panel_<semver>_linux_amd64.zip  (contains cpa-grok-panel.so at zip root)
# Usage: scripts/package_release.sh [version]
# Example: scripts/package_release.sh 0.4.2
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
echo "building cpa-grok-panel.so (linux/amd64 c-shared)..."
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -buildmode=c-shared -o dist/cpa-grok-panel.so .
ZIP="dist/cpa-grok-panel_${VERSION}_linux_amd64.zip"
python3 - <<PY
import hashlib, pathlib, zipfile
version = "$VERSION"
so = pathlib.Path("dist/cpa-grok-panel.so")
zip_path = pathlib.Path(f"dist/cpa-grok-panel_{version}_linux_amd64.zip")
with zipfile.ZipFile(zip_path, "w", compression=zipfile.ZIP_DEFLATED) as zf:
    zf.write(so, arcname="cpa-grok-panel.so")
lines = [
    f"{hashlib.sha256(zip_path.read_bytes()).hexdigest()}  {zip_path.name}",
    f"{hashlib.sha256(so.read_bytes()).hexdigest()}  {so.name}",
]
pathlib.Path("dist/checksums.txt").write_text("\n".join(lines) + "\n")
print(zip_path)
print("\n".join(lines))
PY
echo "done. upload with:"
echo "  gh release upload v${VERSION} ${ZIP} dist/checksums.txt --clobber"
