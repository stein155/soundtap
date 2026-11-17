#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-only
# SPDX-FileCopyrightText: 2026 SoundTap contributors

"""List the known CVEs of the packages in SoundTap Core.

    os_cves.py output/os/sbom.cdx.json

Reads the CycloneDX SBOM `make legal-info` writes and asks the NVD API 2.0 for the CVEs of
each shipped package's CPE: every target package, plus the host packages in SHIPPED_HOST,
whose output goes onto the speaker. Other host packages only build the image. Shipped
packages without a CPE are listed as not checked. CVEs Buildroot marks as ignored for a
package (<PKG>_IGNORE_CVES, listed in the SBOM's `vulnerabilities`) are cleared and printed
apart. Exits 1 when an open CVE scores MIN_SCORE or more, 2 when the SBOM is empty or
malformed or NVD cannot be read. Set NVD_API_KEY for the faster rate limit.
"""
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request

NVD = "https://services.nvd.nist.gov/rest/json/cves/2.0"
MIN_SCORE = 7.0
PAGE = 2000
# NVD allows 5 requests per 30 s without a key and 50 with one.
PAUSE = (0.7, 6.5)
METRICS = ("cvssMetricV40", "cvssMetricV31", "cvssMetricV30", "cvssMetricV2")


# Host packages whose output the STU installs: SPL and U-Boot, and the ST20's BCO image.
SHIPPED_HOST = ("soundtap-bootloaders", "soundtap-bco-app")


def shipped(sbom):
    """The components that end up on the speaker. ValueError when the SBOM cannot be a real build's."""
    comps = sbom.get("components") if isinstance(sbom, dict) else None
    if not isinstance(comps, list):
        raise ValueError("SBOM has no components list")
    targets, hosts = [], {}
    for comp in comps:
        props = {p["name"]: p["value"] for p in comp.get("properties", [])}
        if props.get("BR_TYPE") == "target":
            targets.append(comp)
        elif props.get("BR_TYPE") == "host" and comp.get("name") in SHIPPED_HOST:
            hosts[comp["name"]] = comp
    if not targets:
        raise ValueError("SBOM has no target packages")
    missing = [name for name in SHIPPED_HOST if name not in hosts]
    if missing:
        raise ValueError(f"SBOM lacks the shipped host packages {', '.join(missing)}")
    return targets + list(hosts.values())


def packages(sbom):
    """The shipped packages with a CPE, one per CPE: {cpe: (name, version, bom-ref)}."""
    found = {}
    for comp in shipped(sbom):
        if comp.get("cpe"):
            found.setdefault(comp["cpe"], (comp["name"], comp.get("version", ""), comp.get("bom-ref", comp["name"])))
    return found


def uncovered(sbom):
    """The shipped packages NVD cannot be asked about, because they have no CPE."""
    return sorted({comp["name"] for comp in shipped(sbom) if not comp.get("cpe")})


def cleared(sbom):
    """{package bom-ref: set of CVE ids} that Buildroot ignores for it."""
    out = {}
    for vuln in sbom.get("vulnerabilities", []):
        for ref in vuln.get("affects", []):
            out.setdefault(ref["ref"], set()).add(vuln["id"])
    return out


def score(cve):
    """The CVE's base score, newest CVSS version first; None when NVD has not scored it yet."""
    metrics = cve.get("metrics", {})
    for key in METRICS:
        for metric in metrics.get(key, []):
            return float(metric["cvssData"]["baseScore"])
    return None


def serious(cve):
    """An unscored CVE is undecided, so it counts until it is scored or waived."""
    s = score(cve)
    return s is None or s >= MIN_SCORE


def fetch(cpe, start):
    query = urllib.parse.urlencode({"cpeName": cpe, "startIndex": start, "resultsPerPage": PAGE})
    # isVulnerable takes no value: only CVEs that make this CPE vulnerable, not ones it merely runs beside.
    req = urllib.request.Request(f"{NVD}?{query}&isVulnerable", headers={"User-Agent": "soundtap-os-cves"})
    key = os.environ.get("NVD_API_KEY")
    if key:
        req.add_header("apiKey", key)
    for attempt in range(5):
        try:
            with urllib.request.urlopen(req, timeout=60) as resp:
                return json.load(resp)
        except urllib.error.HTTPError as err:
            # NVD answers 404 for a CPE name it has never seen. That is not "no CVEs": a
            # CVE with a version range can still cover it, so the package is not checked.
            if err.code == 404:
                return None
            if attempt == 4 or err.code not in (403, 429, 500, 502, 503, 504):
                raise
        except (urllib.error.URLError, TimeoutError):
            if attempt == 4:
                raise
        time.sleep(30)
    raise AssertionError("unreachable")


def cves(cpe, get=fetch, pause=0.0):
    """Every non-rejected CVE NVD lists for the CPE, paged; None when NVD does not know the CPE."""
    found, start = [], 0
    while True:
        page = get(cpe, start)
        if page is None:
            return None
        found += [v["cve"] for v in page.get("vulnerabilities", []) if v["cve"].get("vulnStatus") != "Rejected"]
        start += PAGE
        if start >= page.get("totalResults", 0):
            return found
        time.sleep(pause)


def check(sbom, get=fetch, pause=0.0, out=sys.stdout):
    """Print open and cleared CVEs per package; return the number of open ones at MIN_SCORE or above."""
    checked = packages(sbom)  # first: it refuses an SBOM that cannot be a real build's
    ignored = cleared(sbom)
    count = 0
    done, unknown = [], []
    print(f"{len(checked)} package(s) checked: {', '.join(sorted(n for n, _, _ in checked.values()))}", file=out)
    print(f"No CPE, not checked: {', '.join(uncovered(sbom)) or 'none'}", file=out)
    for cpe, (name, version, ref) in sorted(checked.items(), key=lambda kv: kv[1]):
        listed = cves(cpe, get, pause)
        time.sleep(pause)
        if listed is None:
            unknown.append(f"{name} {version}")
            continue
        found = sorted(listed, key=lambda c: (-(score(c) if score(c) is not None else 99.0), c["id"]))
        mine = ignored.get(ref, set())
        open_ = [c for c in found if c["id"] not in mine]
        done += [(name, c["id"]) for c in found if c["id"] in mine]
        if not open_:
            continue
        print(f"{name} {version}: {len(open_)} open", file=out)
        for c in open_:
            s = score(c)
            print(f"  {c['id']}  {'unscored' if s is None else f'{s:4.1f}'}", file=out)
        count += sum(1 for c in open_ if serious(c))
    if done:
        print("Cleared by Buildroot:", file=out)
        for name, cve in done:
            print(f"  {name} {cve}", file=out)
    print(f"CPE unknown to NVD, not checked: {', '.join(unknown) or 'none'}", file=out)
    print(f"{count} open CVE(s) scoring {MIN_SCORE} or more, or not scored yet", file=out)
    return count


def main(argv):
    if len(argv) != 2:
        print(__doc__.strip(), file=sys.stderr)
        return 2
    with open(argv[1]) as f:
        try:
            sbom = json.load(f)
            shipped(sbom)
        except ValueError as err:  # JSONDecodeError is one too
            print(f"SBOM: {err}", file=sys.stderr)
            return 2
    pause = PAUSE[0] if os.environ.get("NVD_API_KEY") else PAUSE[1]
    try:
        return 1 if check(sbom, pause=pause) else 0
    except (urllib.error.URLError, OSError) as err:
        print(f"NVD: {err}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main(sys.argv))
