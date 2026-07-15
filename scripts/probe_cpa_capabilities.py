#!/usr/bin/env python3
"""Probe CPA management APIs for cpa-grok-panel design gate.

Reads CPA_BASE_URL and CPA_MANAGEMENT_KEY from environment
(or from /root/.hermes/.env). Never prints the key.
"""
from __future__ import annotations

import json
import os
import sys
import urllib.error
import urllib.request
from pathlib import Path
from typing import Any


def load_dotenv(path: str = "/root/.hermes/.env") -> None:
    p = Path(path)
    if not p.exists():
        return
    for line in p.read_text().splitlines():
        line = line.strip()
        if not line or line.startswith("#") or "=" not in line:
            continue
        k, v = line.split("=", 1)
        k, v = k.strip(), v.strip().strip('"').strip("'")
        if k and k not in os.environ:
            os.environ[k] = v


def req(
    method: str,
    url: str,
    key: str,
    body: dict | None = None,
    timeout: float = 20.0,
) -> tuple[int, Any, str]:
    data = None
    headers = {
        "Authorization": f"Bearer {key}",
        "Accept": "application/json",
        "User-Agent": "cpa-grok-panel-probe/0.1",
    }
    if body is not None:
        raw = json.dumps(body).encode()
        data = raw
        headers["Content-Type"] = "application/json"
    r = urllib.request.Request(url, data=data, headers=headers, method=method)
    try:
        with urllib.request.urlopen(r, timeout=timeout) as resp:
            text = resp.read().decode("utf-8", errors="replace")
            try:
                return resp.status, json.loads(text) if text else None, text[:500]
            except json.JSONDecodeError:
                return resp.status, None, text[:500]
    except urllib.error.HTTPError as e:
        text = e.read().decode("utf-8", errors="replace")
        try:
            parsed = json.loads(text) if text else None
        except json.JSONDecodeError:
            parsed = None
        return e.code, parsed, text[:500]
    except Exception as e:
        return -1, None, f"{type(e).__name__}: {e}"


def redact_sample(obj: Any, depth: int = 0) -> Any:
    """Return structure sample without secrets."""
    secret_keys = {
        "access_token",
        "refresh_token",
        "id_token",
        "api_key",
        "apikey",
        "authorization",
        "cookie",
        "password",
        "secret",
        "token",
        "client_secret",
    }
    if depth > 4:
        return "..."
    if isinstance(obj, dict):
        out = {}
        for k, v in list(obj.items())[:40]:
            lk = str(k).lower()
            if any(s in lk for s in secret_keys):
                out[k] = "<redacted>"
            else:
                out[k] = redact_sample(v, depth + 1)
        if len(obj) > 40:
            out["__truncated_keys__"] = len(obj) - 40
        return out
    if isinstance(obj, list):
        if not obj:
            return []
        return [redact_sample(obj[0], depth + 1), f"... total={len(obj)}"]
    if isinstance(obj, str) and len(obj) > 120:
        return obj[:120] + "..."
    return obj


def field_presence(sample: dict) -> dict[str, bool]:
    keys = {
        "auth_index",
        "id",
        "name",
        "path",
        "provider",
        "type",
        "priority",
        "disabled",
        "status",
        "email",
        "label",
        "revision",
        "updated_at",
        "file_name",
        "auth_file_id",
    }
    flat = {str(k).lower(): True for k in sample.keys()}
    return {k: k in flat or any(k in x for x in flat) for k in sorted(keys)}


def main() -> int:
    load_dotenv()
    base = (os.environ.get("CPA_BASE_URL") or "").strip().rstrip("/")
    key = (os.environ.get("CPA_MANAGEMENT_KEY") or "").strip()
    if not base or not key:
        print("CPA_BASE_URL or CPA_MANAGEMENT_KEY is empty.")
        print("Fill them in /root/.hermes/.env then re-run.")
        print(f"  CPA_BASE_URL={'set' if base else 'EMPTY'}")
        print(f"  CPA_MANAGEMENT_KEY={'set' if key else 'EMPTY'}")
        return 2

    print(f"Target base: {base}")
    print(f"Key length: {len(key)} (not printed)")
    print("=" * 60)

    results: dict[str, Any] = {"base": base, "probes": {}}

    probes = [
        ("GET", "/v0/management/auth-files", None, "auth_files_list"),
        ("GET", "/v0/management/config", None, "config"),
        ("GET", "/v0/management/version", None, "version"),
        ("GET", "/v0/management/plugins", None, "plugins_list"),
        ("GET", "/v0/management/usage", None, "usage_endpoint"),
        ("GET", "/v0/management/request-error-logs", None, "error_logs"),
    ]

    for method, path, body, name in probes:
        status, data, snip = req(method, base + path, key, body)
        entry = {
            "method": method,
            "path": path,
            "status": status,
            "ok": 200 <= status < 300 if status > 0 else False,
            "sample": redact_sample(data) if data is not None else snip[:200],
        }
        results["probes"][name] = entry
        print(f"\n[{name}] {method} {path} -> HTTP {status}")
        if isinstance(data, dict):
            print("  top keys:", sorted(list(data.keys()))[:30])
        elif isinstance(data, list):
            print(f"  list len: {len(data)}")
            if data and isinstance(data[0], dict):
                print("  item0 keys:", sorted(list(data[0].keys()))[:40])

    # deeper auth-files analysis
    af = results["probes"].get("auth_files_list", {})
    data = af.get("sample")
    items = None
    if isinstance(data, dict):
        for k in ("files", "items", "data", "auth_files", "result"):
            if isinstance(data.get(k), list):
                items = data[k]
                break
        if items is None and any(isinstance(v, list) for v in data.values()):
            for v in data.values():
                if isinstance(v, list):
                    items = v
                    break
    elif isinstance(data, list):
        items = data

    auth_analysis: dict[str, Any] = {"count": 0}
    if isinstance(items, list) and items:
        # sample is already redacted first item only in redact_sample for lists
        # re-fetch one structured view
        status, raw, _ = req("GET", base + "/v0/management/auth-files", key)
        real_items = None
        if isinstance(raw, dict):
            for k in ("files", "items", "data", "auth_files"):
                if isinstance(raw.get(k), list):
                    real_items = raw[k]
                    break
        elif isinstance(raw, list):
            real_items = raw
        if real_items:
            auth_analysis["count"] = len(real_items)
            # find first xai-like and any first
            sample_item = real_items[0] if real_items else {}
            xai = None
            for it in real_items:
                if not isinstance(it, dict):
                    continue
                blob = json.dumps({k: it.get(k) for k in it if k.lower() not in (
                    "access_token", "refresh_token", "id_token", "api_key"
                )}, ensure_ascii=False).lower()
                if "xai" in blob or "grok" in blob:
                    xai = it
                    break
            pick = xai or sample_item
            if isinstance(pick, dict):
                auth_analysis["fields"] = field_presence(pick)
                auth_analysis["sample_redacted"] = redact_sample(pick)
                # identity candidates
                auth_analysis["identity_candidates"] = {
                    "auth_index": pick.get("auth_index") or pick.get("authIndex"),
                    "id": pick.get("id"),
                    "name": pick.get("name"),
                    "provider": pick.get("provider") or pick.get("type"),
                    "priority": pick.get("priority"),
                    "disabled": pick.get("disabled"),
                    "has_revision": "revision" in pick or "updated_at" in pick,
                }
    results["auth_analysis"] = auth_analysis

    # Probe write-like endpoints carefully: OPTIONS/PATCH dry checks with invalid body to see if route exists
    write_probes = [
        ("PATCH", "/v0/management/auth-files/status", {"name": "__probe_nonexistent__.json", "disabled": True}, "patch_status_route"),
        ("PATCH", "/v0/management/auth-files/fields", {"name": "__probe_nonexistent__.json", "priority": -100}, "patch_fields_route"),
        ("DELETE", "/v0/management/auth-files", {"names": ["__probe_nonexistent__.json"]}, "delete_auth_route"),
    ]
    print("\n" + "=" * 60)
    print("Write-route existence probes (intentionally invalid targets):")
    for method, path, body, name in write_probes:
        status, data, snip = req(method, base + path, key, body)
        results["probes"][name] = {
            "method": method,
            "path": path,
            "status": status,
            "route_exists": status not in (404, -1),
            "sample": redact_sample(data) if data is not None else snip[:200],
        }
        print(f"  [{name}] {method} {path} -> HTTP {status} (route_exists={status not in (404, -1)})")

    # capability mapping for design
    print("\n" + "=" * 60)
    print("Design capability mapping (heuristic):")
    mapping = {
        "auth_list": results["probes"].get("auth_files_list", {}).get("ok"),
        "stable_auth_index_field": bool(
            (auth_analysis.get("identity_candidates") or {}).get("auth_index")
            or (auth_analysis.get("fields") or {}).get("auth_index")
        ),
        "priority_field_present": bool(
            (auth_analysis.get("fields") or {}).get("priority")
            or (auth_analysis.get("identity_candidates") or {}).get("priority") is not None
        ),
        "revision_field_present": bool((auth_analysis.get("fields") or {}).get("revision")),
        "set_enabled_via_status": results["probes"].get("patch_status_route", {}).get("route_exists"),
        "set_priority_via_fields": results["probes"].get("patch_fields_route", {}).get("route_exists"),
        "delete_auth": results["probes"].get("delete_auth_route", {}).get("route_exists"),
        "plugins_api": results["probes"].get("plugins_list", {}).get("ok")
        or results["probes"].get("plugins_list", {}).get("status") not in (404, -1, None),
        "usage_mgmt_endpoint": results["probes"].get("usage_endpoint", {}).get("ok")
        or results["probes"].get("usage_endpoint", {}).get("status") not in (404, -1, None),
    }
    for k, v in mapping.items():
        print(f"  {k}: {v}")
    results["capability_heuristic"] = mapping

    # notes for event_id / usage plugin — cannot fully prove via management REST
    results["notes"] = [
        "usage.handle / event_id / Failed attribution require a live plugin load + traffic; not fully provable by management REST alone.",
        "host.auth.get/save are plugin-host callbacks; management PATCH/DELETE approximate write capabilities.",
        "Do not treat 4xx on invalid probe names as 'unsupported' if status is 400/404/422 with JSON body — route exists.",
    ]
    for n in results["notes"]:
        print("NOTE:", n)

    out = Path("/root/codes/cpa-grok-panel/docs/reviews/cpa-capability-probe.json")
    out.parent.mkdir(parents=True, exist_ok=True)
    out.write_text(json.dumps(results, ensure_ascii=False, indent=2) + "\n")
    print(f"\nWrote {out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
