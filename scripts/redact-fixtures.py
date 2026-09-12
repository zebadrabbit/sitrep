#!/usr/bin/env python3
"""Redact captured fixtures in place (HANDOFF §9).

LAN 192.168.1.x → 10.0.0.x, VPN 10.8.0.x → 10.0.1.x, public IPv4 → 203.0.113.N,
global IPv6 → 2001:db8::N, link-local → fe80::N, MAC → OUI kept, device half zeroed,
hostname → example, user → user.
Docker bridge 172.17-24 stays: it is a default, not a secret.
Run after: SITREP_CAPTURE_FIXTURES=1 sitrep snapshot
"""
import ipaddress, os, re, sys

ROOT = sys.argv[1] if len(sys.argv) > 1 else "testdata/fixtures"
HOST, USER = os.uname().nodename.split(".")[0], os.environ.get("USER", "winter")
pub4, pub6, ll6, macs = {}, {}, {}, {}

def ip4(m):
    s = m.group(0)
    try: a = ipaddress.ip_address(s)
    except ValueError: return s
    if s.startswith("192.168.1."): return "10.0.0." + s.rsplit(".", 1)[1]
    if s.startswith("10.8.0."): return "10.0.1." + s.rsplit(".", 1)[1]
    if a.is_private or a.is_loopback or a.is_multicast or a.is_unspecified or a.is_link_local or a.is_reserved: return s
    return pub4.setdefault(s, f"203.0.113.{len(pub4)+1}")

def ip6(m):
    s = m.group(0)
    try: a = ipaddress.ip_address(s)
    except ValueError: return s
    if a.is_link_local: return ll6.setdefault(s, f"fe80::{len(ll6)+1}")
    if a.is_global: return pub6.setdefault(s, f"2001:db8::{len(pub6)+1}")
    return s

def mac(m):
    """Zero the device half, keep the OUI.

    The last three octets are what identifies one box; the first three are a
    public IEEE registry entry. Keeping them lets the lan module's vendor
    lookup and its locally-administered-bit check still mean something in
    --demo, which is also how that parser gets exercised.
    """
    s = m.group(0).lower()
    if s == "00:00:00:00:00:00": return m.group(0)
    return macs.setdefault(s, f"{s[:8]}:00:00:{len(macs)+1:02x}")

R4 = re.compile(r"\b(?:\d{1,3}\.){3}\d{1,3}\b")
# {1,7}: "fe80::x" has a single hex group before the "::"; {2,} let EUI-64 link-locals through.
R6 = re.compile(r"(?<![0-9a-fA-F:])(?:[0-9a-fA-F]{1,4}:){1,7}[0-9a-fA-F:]*[0-9a-fA-F](?![0-9a-fA-F:])")
RM = re.compile(r"\b(?:[0-9a-fA-F]{2}:){5}[0-9a-fA-F]{2}\b")
n = 0
for dp, _, files in os.walk(ROOT):
    for f in files:
        p = os.path.join(dp, f)
        try: s = open(p, encoding="utf-8", errors="surrogateescape").read()
        except OSError: continue
        t = RM.sub(mac, R6.sub(ip6, R4.sub(ip4, s))).replace(HOST, "example").replace(f"/home/{USER}", "/home/user").replace(USER, "user")
        if t != s:
            open(p, "w", encoding="utf-8", errors="surrogateescape").write(t); n += 1
print(f"redacted {n} files; {len(pub4)} public v4, {len(pub6)} global v6, {len(ll6)} link-local, {len(macs)} MACs")
