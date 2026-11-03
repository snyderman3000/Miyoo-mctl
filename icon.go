package main

import (
	"image"
	"image/color"
	"math"
)

// appIcon draws the OnionOS app icon: a phosphor globe with an orbit and a spacecraft.
func appIcon(sz int) image.Image {
	const ss = 4
	n := sz * ss
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	th := themes[0]
	r := float64(n) / 2
	corner := float64(n) * 0.2
	globeR := r * 0.5
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			fx, fy := float64(x)+0.5, float64(y)+0.5
			// rounded tile
			dx := math.Max(0, math.Max(corner-fx, fx-(float64(n)-corner)))
			dy := math.Max(0, math.Max(corner-fy, fy-(float64(n)-corner)))
			if dx*dx+dy*dy > corner*corner {
				continue
			}
			col := th.BG
			cx, cy := fx-r, fy-r
			d := math.Hypot(cx, cy)
			if d < globeR {
				z := math.Sqrt(1 - (d/globeR)*(d/globeR))
				lat := math.Asin(-cy/globeR) / deg
				lon := math.Atan2(cx/globeR, z)/deg - 95
				col = th.Sea
				if isLand(lat, lon) {
					col = th.Land
				}
				if cx/globeR > 0.2 { // night side
					col = shade(col, 0.6)
				}
				col = shade(col, 0.6+0.4*z)
				if d > globeR-float64(ss)*1.2 {
					col = th.Coast
				}
			}
			// orbit: tilted ellipse
			a, b, tilt := r*0.82, r*0.3, -25*deg
			ex := cx*math.Cos(tilt) + cy*math.Sin(tilt)
			ey := -cx*math.Sin(tilt) + cy*math.Cos(tilt)
			e := math.Hypot(ex/a, ey/b)
			if math.Abs(e-1)*b < float64(ss)*0.9 && (ey > 0 || d > globeR) {
				col = th.FG
			}
			// spacecraft at the front of the orbit
			sxp, syp := a*math.Cos(0.9), b*math.Sin(0.9)
			sx := sxp*math.Cos(tilt) - syp*math.Sin(tilt)
			sy := sxp*math.Sin(tilt) + syp*math.Cos(tilt)
			if math.Abs(cx-sx) < float64(ss)*3.2 && math.Abs(cy-sy) < float64(ss)*3.2 {
				col = th.Hi
			}
			if math.Abs(cy-sy) < float64(ss)*1.2 && math.Abs(cx-sx) < float64(ss)*8 {
				col = th.Hi
			}
			img.Set(x, y, col)
		}
	}
	// downsample for smooth edges
	out := image.NewRGBA(image.Rect(0, 0, sz, sz))
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			var R, G, B, A int
			for j := 0; j < ss; j++ {
				for i := 0; i < ss; i++ {
					p := img.RGBAAt(x*ss+i, y*ss+j)
					R += int(p.R)
					G += int(p.G)
					B += int(p.B)
					A += int(p.A)
				}
			}
			k := ss * ss
			out.Set(x, y, color.RGBA{uint8(R / k), uint8(G / k), uint8(B / k), uint8(A / k)})
		}
	}
	return out
}
