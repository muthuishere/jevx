#!/usr/bin/env python3
"""check_links.py DIST [BASE]: fail when a built page links to a missing page, file or #anchor under BASE (default /jevx/)."""
import glob, html, os, re, sys

dist = sys.argv[1] if len(sys.argv) > 1 else "site/dist"
base = sys.argv[2] if len(sys.argv) > 2 else "/jevx/"
pages = glob.glob(os.path.join(dist, "**", "*.html"), recursive=True)
ids = {p: set(re.findall(r'id="([^"]+)"', open(p, encoding="utf-8").read())) for p in pages}
bad, n = set(), 0
for p in pages:
    for h in re.findall(r'href="([^"]+)"', open(p, encoding="utf-8").read()):
        h = html.unescape(h)
        if not h.startswith(base):
            continue
        n += 1
        path, _, frag = h.partition("#")
        t = os.path.join(dist, path.split("?")[0][len(base):])
        if t.endswith("/") or os.path.isdir(t):
            t = os.path.join(t, "index.html")
        if not os.path.exists(t):
            bad.add(f"{os.path.relpath(p, dist)}: {h} (missing)")
        elif frag and t.endswith(".html") and frag not in ids.get(t, set()):
            bad.add(f"{os.path.relpath(p, dist)}: {h} (no #{frag})")
for b in sorted(bad):
    print("broken", b)
print(f"{n} internal links in {len(pages)} pages, {len(bad)} broken")
sys.exit(1 if bad or not pages else 0)
