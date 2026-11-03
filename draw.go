package main

// Drawing primitives and the three charts: flat map, globe and sky plot.

import (
	_ "embed"
	"image/color"
	"math"
	"time"
)

//go:embed assets/land.bin
var landBits []byte

const landW, landH = 1440, 720

// isLand samples the Natural Earth mask.
func isLand(lat, lon float64) bool {
	x := int((lon + 180) / 360 * landW)
	y := int((90 - lat) / 180 * landH)
	if x < 0 {
		x += landW
	}
	if x >= landW {
		x -= landW
	}
	if y < 0 {
		y = 0
	}
	if y >= landH {
		y = landH - 1
	}
	i := y*landW + x
	return landBits[i>>3]&(1<<(i&7)) != 0
}

// ---------- primitives ----------

func (c *Canvas) Blend(x, y int, col color.RGBA, a float64) {
	if x < 0 || y < 0 || x >= c.W || y >= c.H || a <= 0 {
		return
	}
	if a > 1 {
		a = 1
	}
	o := y*c.Stride + x*4
	k := int(a * 256)
	c.Pix[o] = uint8((int(c.Pix[o])*(256-k) + int(col.R)*k) >> 8)
	c.Pix[o+1] = uint8((int(c.Pix[o+1])*(256-k) + int(col.G)*k) >> 8)
	c.Pix[o+2] = uint8((int(c.Pix[o+2])*(256-k) + int(col.B)*k) >> 8)
}

// Line draws a 1px line; dash>0 makes it dashed (on/off of dash pixels).
func (c *Canvas) Line(x0, y0, x1, y1 int, col color.RGBA, dash int) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	n := 0
	for {
		if dash == 0 || (n/dash)%2 == 0 {
			c.Set(x0, y0, col)
		}
		n++
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func (c *Canvas) ThickLine(x0, y0, x1, y1 int, col color.RGBA) {
	c.Line(x0, y0, x1, y1, col, 0)
	if abs(x1-x0) > abs(y1-y0) {
		c.Line(x0, y0+1, x1, y1+1, col, 0)
	} else {
		c.Line(x0+1, y0, x1+1, y1, col, 0)
	}
}

func (c *Canvas) Circle(cx, cy, r int, col color.RGBA, dash int) {
	n := int(2*math.Pi*float64(r)) + 8
	for i := 0; i < n; i++ {
		if dash > 0 && (i/dash)%2 == 1 {
			continue
		}
		a := 2 * math.Pi * float64(i) / float64(n)
		c.Set(cx+int(math.Round(float64(r)*math.Cos(a))), cy+int(math.Round(float64(r)*math.Sin(a))), col)
	}
}

func (c *Canvas) Disc(cx, cy, r int, col color.RGBA) {
	for y := -r; y <= r; y++ {
		for x := -r; x <= r; x++ {
			if x*x+y*y <= r*r+r/2 {
				c.Set(cx+x, cy+y, col)
			}
		}
	}
}

// Panel is a titled box in the console style.
func (c *Canvas) Panel(th Theme, f *Font, x, y, w, h int, title string) {
	c.Fill(x, y, w, h, th.Panel)
	c.Border(x, y, w, h, 1, th.Faint)
	if title != "" {
		tw := f.Width(title) + 10
		c.Fill(x+8, y-1, tw, 2, th.Panel)
		c.Fill(x+6, y, 2, 1, th.Dim)
		c.Text(f, x+13, y-f.height/2+1, title, th.Dim)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func shade(a color.RGBA, k float64) color.RGBA {
	return color.RGBA{uint8(float64(a.R) * k), uint8(float64(a.G) * k), uint8(float64(a.B) * k), 255}
}

// ---------- flat map ----------

// MapView places an equirectangular map inside a rectangle.
type MapView struct{ X, Y, W, H int }

func (m MapView) XY(lat, lon float64) (float64, float64) {
	return float64(m.X) + (lon+180)/360*float64(m.W), float64(m.Y) + (90-lat)/180*float64(m.H)
}

// baseMap renders sea, land, coastlines and graticule once per theme and size.
func baseMap(th Theme, w, h int) *Canvas {
	c := NewCanvas(w, h)
	land := make([]bool, w*h)
	for y := 0; y < h; y++ {
		lat := 90 - (float64(y)+0.5)/float64(h)*180
		for x := 0; x < w; x++ {
			land[y*w+x] = isLand(lat, (float64(x)+0.5)/float64(w)*360-180)
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			col := th.Sea
			if land[y*w+x] {
				col = th.Land
				// coast: a land pixel touching sea
				for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					xx, yy := (x+d[0]+w)%w, y+d[1]
					if yy >= 0 && yy < h && !land[yy*w+xx] {
						col = th.Coast
						break
					}
				}
			}
			c.Set(x, y, col)
		}
	}
	for lon := -150; lon < 180; lon += 30 {
		x := int(float64(lon+180) / 360 * float64(w))
		for y := 0; y < h; y += 2 {
			c.Blend(x, y, th.Grid, 0.9)
		}
	}
	for lat := -60; lat <= 60; lat += 30 {
		y := int(float64(90-lat) / 180 * float64(h))
		for x := 0; x < w; x += 2 {
			c.Blend(x, y, th.Grid, 0.9)
		}
	}
	return c
}

// nightMap darkens the base map where the Sun is down, with a soft twilight band.
func nightMap(th Theme, base *Canvas, sun Geo, dst *Canvas) {
	w, h := base.W, base.H
	sl, cl := math.Sin(sun.Lat*deg), math.Cos(sun.Lat*deg)
	cosCol := make([]float64, w)
	for x := 0; x < w; x++ {
		lon := (float64(x)+0.5)/float64(w)*360 - 180
		cosCol[x] = math.Cos((lon - sun.Lon) * deg)
	}
	copy(dst.Pix, base.Pix)
	for y := 0; y < h; y++ {
		lat := (90 - (float64(y)+0.5)/float64(h)*180) * deg
		a, b := math.Sin(lat)*sl, math.Cos(lat)*cl
		o := y * dst.Stride
		for x := 0; x < w; x++ {
			e := a + b*cosCol[x] // sine of the Sun's elevation
			if e < 0.05 {
				t := clamp((0.05-e)/0.15, 0, 1) // fades in through twilight
				k := int((1 - th.Night*t) * 256)
				p := dst.Pix[o+x*4 : o+x*4+3]
				p[0] = uint8(int(p[0]) * k >> 8)
				p[1] = uint8(int(p[1]) * k >> 8)
				p[2] = uint8(int(p[2]) * k >> 8)
			}
		}
	}
}

// Blit copies src onto c at (x,y).
func (c *Canvas) Blit(src *Canvas, x, y int) {
	for yy := 0; yy < src.H; yy++ {
		if y+yy < 0 || y+yy >= c.H {
			continue
		}
		n := src.W
		if x+n > c.W {
			n = c.W - x
		}
		copy(c.Pix[(y+yy)*c.Stride+x*4:(y+yy)*c.Stride+(x+n)*4], src.Pix[yy*src.Stride:yy*src.Stride+n*4])
	}
}

// polylineMap draws a lat/lon path, breaking it at the antimeridian.
func (c *Canvas) polylineMap(m MapView, pts [][2]float64, col color.RGBA, dash int) {
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		if math.Abs(a[1]-b[1]) > 180 {
			continue
		}
		x0, y0 := m.XY(a[0], a[1])
		x1, y1 := m.XY(b[0], b[1])
		c.Line(int(x0), int(y0), int(x1), int(y1), col, dash)
	}
}

// sunIcon is a small rayed disc.
func (c *Canvas) sunIcon(x, y int, col color.RGBA) {
	c.Disc(x, y, 4, col)
	for i := 0; i < 8; i++ {
		a := float64(i) * math.Pi / 4
		c.Line(x+int(7*math.Cos(a)), y+int(7*math.Sin(a)), x+int(9*math.Cos(a)), y+int(9*math.Sin(a)), col, 0)
	}
}

// crosshair marks the vehicle.
func (c *Canvas) crosshair(x, y int, col, bg color.RGBA, big bool) {
	r := 5
	if big {
		r = 7
	}
	c.Border(x-r, y-r, 2*r+1, 2*r+1, 1, col)
	c.Fill(x-1, y-1, 3, 3, col)
	c.Line(x-r-5, y, x-r-1, y, col, 0)
	c.Line(x+r+1, y, x+r+5, y, col, 0)
	c.Line(x, y-r-5, x, y-r-1, col, 0)
	c.Line(x, y+r+1, x, y+r+5, col, 0)
	_ = bg
}

// siteMark is a diamond for the observer.
func (c *Canvas) siteMark(x, y int, col color.RGBA) {
	for i := -4; i <= 4; i++ {
		w := 4 - abs(i)
		c.Set(x-w, y+i, col)
		c.Set(x+w, y+i, col)
	}
	c.Set(x, y, col)
}

// ---------- globe ----------

// Globe is an orthographic view centred on (Lat, Lon).
type Globe struct {
	CX, CY, R int
	Lat, Lon  float64
	e, n, u   Vec3
}

func newGlobe(cx, cy, r int, lat, lon float64) *Globe {
	g := &Globe{CX: cx, CY: cy, R: r, Lat: lat, Lon: lon}
	la, lo := lat*deg, lon*deg
	g.e = Vec3{-math.Sin(lo), math.Cos(lo), 0}
	g.n = Vec3{-math.Sin(la) * math.Cos(lo), -math.Sin(la) * math.Sin(lo), math.Cos(la)}
	g.u = Vec3{math.Cos(la) * math.Cos(lo), math.Cos(la) * math.Sin(lo), math.Sin(la)}
	return g
}

// Project maps an Earth-fixed vector (in Earth radii) to the screen; front is
// false when the point is hidden behind the planet.
func (g *Globe) Project(p Vec3) (float64, float64, bool) {
	x, y, z := p.Dot(g.e), p.Dot(g.n), p.Dot(g.u)
	front := z >= 0 || x*x+y*y > 1
	return float64(g.CX) + x*float64(g.R), float64(g.CY) - y*float64(g.R), front
}

func unitLL(lat, lon float64) Vec3 {
	la, lo := lat*deg, lon*deg
	return Vec3{math.Cos(la) * math.Cos(lo), math.Cos(la) * math.Sin(lo), math.Sin(la)}
}

// renderGlobe draws the lit, shaded sphere with land, night and graticule.
func renderGlobe(th Theme, g *Globe, sun Geo) *Canvas {
	d := 2*g.R + 1
	c := NewCanvas(d, d)
	c.Fill(0, 0, d, d, th.BG)
	sunV := unitLL(sun.Lat, sun.Lon)
	R := float64(g.R)
	land := make([]bool, d*d)
	inside := make([]bool, d*d)
	lat := make([]float64, d*d)
	lon := make([]float64, d*d)
	for py := 0; py < d; py++ {
		y := -(float64(py) - R) / R
		for px := 0; px < d; px++ {
			x := (float64(px) - R) / R
			rr := x*x + y*y
			if rr > 1 {
				continue
			}
			z := math.Sqrt(1 - rr)
			p := g.e.Scale(x).Add(g.n.Scale(y)).Add(g.u.Scale(z))
			i := py*d + px
			inside[i] = true
			lat[i] = math.Asin(clamp(p[2], -1, 1)) / deg
			lon[i] = math.Atan2(p[1], p[0]) / deg
			land[i] = isLand(lat[i], lon[i])
		}
	}
	for py := 0; py < d; py++ {
		y := -(float64(py) - R) / R
		for px := 0; px < d; px++ {
			i := py*d + px
			if !inside[i] {
				continue
			}
			x := (float64(px) - R) / R
			z := math.Sqrt(math.Max(0, 1-x*x-y*y))
			p := g.e.Scale(x).Add(g.n.Scale(y)).Add(g.u.Scale(z))
			col := th.Sea
			if land[i] {
				col = th.Land
				for _, dd := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
					xx, yy := px+dd[0], py+dd[1]
					if xx >= 0 && yy >= 0 && xx < d && yy < d && inside[yy*d+xx] && !land[yy*d+xx] {
						col = th.Coast
						break
					}
				}
			}
			// graticule every 30°
			if math.Abs(math.Mod(lat[i]+300, 30)) < 0.45 && math.Abs(lat[i]) < 89 ||
				math.Abs(math.Mod(lon[i]+360, 30)) < 0.45/math.Max(0.15, math.Cos(lat[i]*deg)) {
				if col != th.Coast {
					col = mix(col, th.Grid, 0.8)
				}
			}
			e := p.Dot(sunV)
			k := 1.0
			if e < 0.05 {
				k = 1 - th.Night*clamp((0.05-e)/0.15, 0, 1)
			}
			k *= 0.55 + 0.45*z // limb darkening sells the sphere
			c.Set(px, py, shade(col, k))
		}
	}
	c.Circle(g.R, g.R, g.R, th.Dim, 0)
	return c
}

// ---------- sky plot ----------

// SkyPlot is a polar az/el diagram: zenith at the centre, horizon at radius R.
type SkyPlot struct{ CX, CY, R int }

func (s SkyPlot) XY(az, el float64) (int, int) {
	r := (90 - el) / 90 * float64(s.R)
	a := az * deg
	return s.CX + int(math.Round(r*math.Sin(a))), s.CY - int(math.Round(r*math.Cos(a)))
}

func (c *Canvas) skyGrid(th Theme, f *Font, s SkyPlot) {
	c.Disc(s.CX, s.CY, s.R, th.Panel)
	c.Circle(s.CX, s.CY, s.R, th.Dim, 0)
	c.Circle(s.CX, s.CY, s.R*2/3, th.Faint, 3)
	c.Circle(s.CX, s.CY, s.R/3, th.Faint, 3)
	c.Line(s.CX-s.R, s.CY, s.CX+s.R, s.CY, th.Faint, 3)
	c.Line(s.CX, s.CY-s.R, s.CX, s.CY+s.R, th.Faint, 3)
	c.TextCenter(f, s.CX, s.CY-s.R-f.height-1, "N", th.FG)
	c.TextCenter(f, s.CX, s.CY+s.R+1, "S", th.Dim)
	c.Text(f, s.CX+s.R+4, s.CY-f.height/2, "E", th.Dim)
	c.TextRight(f, s.CX-s.R-4, s.CY-f.height/2, "W", th.Dim)
	c.Text(f, s.CX+3, s.CY-s.R/3-f.height+2, "60", th.Faint)
	c.Text(f, s.CX+3, s.CY-s.R*2/3-f.height+2, "30", th.Faint)
}

func fmtDur(d time.Duration) string {
	neg := d < 0
	if neg {
		d = -d
	}
	s := int(d.Round(time.Second) / time.Second)
	out := ""
	if s >= 86400 {
		out = itoa(s/86400) + "d " + pad2(s%86400/3600) + ":" + pad2(s%3600/60) + ":" + pad2(s%60)
	} else {
		out = pad2(s/3600) + ":" + pad2(s%3600/60) + ":" + pad2(s%60)
	}
	if neg {
		return "-" + out
	}
	return out
}

func pad2(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
