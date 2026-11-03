# Rasterizes Natural Earth land (via world-atlas topojson) into a 1-bit
# equirectangular mask: assets/land.bin, W x H bits, row 0 = 90N, col 0 = 180W.
import json, sys
from PIL import Image, ImageDraw
src, out, W = sys.argv[1], sys.argv[2], int(sys.argv[3])
H = W // 2
t = json.load(open(src))
sc, tr = t["transform"]["scale"], t["transform"]["translate"]
arcs = []
for a in t["arcs"]:
    x = y = 0; pts = []
    for dx, dy in a:
        x += dx; y += dy
        pts.append((x * sc[0] + tr[0], y * sc[1] + tr[1]))
    arcs.append(pts)
def ring(idx):
    pts = []
    for i in idx:
        p = arcs[i] if i >= 0 else arcs[~i][::-1]
        pts.extend(p if not pts else p[1:])
    return pts
SS = 4
img = Image.new("L", (W * SS, H * SS), 0)
d = ImageDraw.Draw(img)
def px(p): return ((p[0] + 180) / 360 * W * SS, (90 - p[1]) / 180 * H * SS)
for g in t["objects"]["land"]["geometries"]:
    polys = g["arcs"] if g["type"] == "MultiPolygon" else [g["arcs"]]
    for poly in polys:
        for k, r in enumerate(poly):
            pts = ring(r)
            # unwrap across the antimeridian, then draw three copies
            u = [pts[0]]
            for lon, lat in pts[1:]:
                pl = u[-1][0]
                while lon - pl > 180: lon -= 360
                while lon - pl < -180: lon += 360
                u.append((lon, lat))
            span = u[-1][0] - u[0][0]
            if abs(span) > 180:  # circles a pole (Antarctica): close via the pole
                pole = -90 if sum(p[1] for p in u) < 0 else 90
                u += [(u[-1][0], pole), (u[0][0], pole)]
            for off in (-360, 0, 360):
                d.polygon([px((lo + off, la)) for lo, la in u], fill=255 if k == 0 else 0)
d.rectangle([0, px((0, -85))[1], W * SS, H * SS], fill=255)  # interior of Antarctica
img = img.resize((W, H), Image.BOX).point(lambda v: 255 if v >= 128 else 0).convert("1")
img.save(out + ".png")
bits = bytearray((W * H + 7) // 8)
pix = img.load()
for y in range(H):
    for x in range(W):
        if pix[x, y]:
            i = y * W + x
            bits[i >> 3] |= 1 << (i & 7)
open(out, "wb").write(bits)
print(W, H, len(bits))
