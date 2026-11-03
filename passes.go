package main

// Pass prediction and orbital events (AOS/LOS, eclipse entry/exit).

import (
	"math"
	"time"
)

// SkyPt is one point of a pass track on the sky plot.
type SkyPt struct {
	Az, El float64
	Lit    bool // the vehicle is in sunlight
}

// Pass is one rise-to-set over the observer.
type Pass struct {
	AOS, TCA, LOS     time.Time
	MaxEl             float64
	AOSAz, MaxAz, LOSAz float64
	Visible           bool // naked-eye: vehicle sunlit, sky dark, above 10°
	VisStart, VisEnd  time.Time
	Track             []SkyPt
}

func (p Pass) Duration() time.Duration { return p.LOS.Sub(p.AOS) }

func elevationAt(t *TLE, site Geo, when time.Time) float64 {
	r, _, err := t.At(when)
	if err != nil {
		return -90
	}
	return lookAngles(site, temeToECEF(r, when), Vec3{}).El
}

// bisect finds the time in [a,b] where f changes sign, to ~1 s.
func bisect(a, b time.Time, f func(time.Time) bool) time.Time {
	fa := f(a)
	for b.Sub(a) > time.Second {
		m := a.Add(b.Sub(a) / 2)
		if f(m) == fa {
			a = m
		} else {
			b = m
		}
	}
	return b
}

// PredictPasses lists passes starting after from (a pass in progress is included)
// within the horizon, stopping after max passes.
func PredictPasses(t *TLE, site Geo, from time.Time, horizon time.Duration, max int) []Pass {
	step := 30 * time.Second
	if p := t.PeriodMin(); p > 200 {
		step = time.Minute
	}
	up := func(w time.Time) bool { return elevationAt(t, site, w) > 0 }
	var out []Pass
	end := from.Add(horizon)
	cur := from
	prev := up(cur)
	var aos time.Time
	if prev {
		// already above the horizon: walk back to find the rise
		b := cur
		for i := 0; i < 60 && up(b); i++ {
			b = b.Add(-step)
		}
		aos = bisect(b, b.Add(step), up)
	}
	for cur.Before(end) && len(out) < max {
		next := cur.Add(step)
		now := up(next)
		if now && !prev {
			aos = bisect(cur, next, up)
		}
		if !now && prev && !aos.IsZero() {
			los := bisect(cur, next, up)
			out = append(out, describePass(t, site, aos, los))
		}
		prev = now
		cur = next
	}
	return out
}

func describePass(t *TLE, site Geo, aos, los time.Time) Pass {
	p := Pass{AOS: aos, LOS: los}
	// golden-section search for the highest point
	a, b := aos, los
	phi := (math.Sqrt(5) - 1) / 2
	for b.Sub(a) > time.Second {
		d := time.Duration(float64(b.Sub(a)) * phi)
		c1, c2 := b.Add(-d), a.Add(d)
		if elevationAt(t, site, c1) > elevationAt(t, site, c2) {
			b = c2
		} else {
			a = c1
		}
	}
	p.TCA = a.Add(b.Sub(a) / 2)
	look := func(w time.Time) (Look, bool) {
		r, _, err := t.At(w)
		if err != nil {
			return Look{}, false
		}
		return lookAngles(site, temeToECEF(r, w), Vec3{}), shadow(r, w) != Umbra
	}
	l, _ := look(p.TCA)
	p.MaxEl, p.MaxAz = l.El, l.Az
	l, _ = look(aos)
	p.AOSAz = l.Az
	l, _ = look(los)
	p.LOSAz = l.Az
	n := 48
	for i := 0; i <= n; i++ {
		w := aos.Add(time.Duration(float64(los.Sub(aos)) * float64(i) / float64(n)))
		l, lit := look(w)
		p.Track = append(p.Track, SkyPt{Az: l.Az, El: math.Max(0, l.El), Lit: lit})
		if lit && l.El >= 10 && sunElevation(site, w) < -6 {
			if !p.Visible {
				p.VisStart = w
			}
			p.Visible = true
			p.VisEnd = w
		}
	}
	return p
}

// NextShadowChange finds when the vehicle next enters (or leaves) Earth's umbra.
func NextShadowChange(t *TLE, from time.Time) (time.Time, bool, bool) {
	in := func(w time.Time) bool {
		r, _, err := t.At(w)
		return err == nil && shadow(r, w) == Umbra
	}
	start := in(from)
	step := 20 * time.Second
	limit := from.Add(time.Duration(t.PeriodMin()*2) * time.Minute)
	for cur := from; cur.Before(limit); cur = cur.Add(step) {
		if in(cur.Add(step)) != start {
			return bisect(cur, cur.Add(step), in), !start, true
		}
	}
	return time.Time{}, false, false // full-sun orbit (high beta)
}

// Track is a ground track: sampled sub-points around a time.
type TrackPt struct {
	Lat, Lon float64
	Lit      bool
	T        time.Time
}

func GroundTrack(t *TLE, from, to time.Time, step time.Duration) []TrackPt {
	var out []TrackPt
	for w := from; !w.After(to); w = w.Add(step) {
		r, _, err := t.At(w)
		if err != nil {
			continue
		}
		g := ecefToGeo(temeToECEF(r, w))
		out = append(out, TrackPt{g.Lat, g.Lon, shadow(r, w) != Umbra, w})
	}
	return out
}

// footprint returns points on the horizon circle (elevation 0) around a sub-point.
func footprint(g Geo, n int) [][2]float64 {
	rad := math.Acos(reWGS84 / (reWGS84 + g.Alt))
	la1, lo1 := g.Lat*deg, g.Lon*deg
	pts := make([][2]float64, 0, n+1)
	for i := 0; i <= n; i++ {
		brg := twoPi * float64(i) / float64(n)
		la2 := math.Asin(math.Sin(la1)*math.Cos(rad) + math.Cos(la1)*math.Sin(rad)*math.Cos(brg))
		lo2 := lo1 + math.Atan2(math.Sin(brg)*math.Sin(rad)*math.Cos(la1), math.Cos(rad)-math.Sin(la1)*math.Sin(la2))
		pts = append(pts, [2]float64{la2 / deg, normLon(lo2 / deg)})
	}
	return pts
}

func normLon(l float64) float64 {
	l = math.Mod(l+180, 360)
	if l < 0 {
		l += 360
	}
	return l - 180
}

// compass turns an azimuth into a 16-point bearing.
func compass(az float64) string {
	pts := []string{"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE", "S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW"}
	return pts[int(math.Mod(az+11.25, 360)/22.5)%16]
}
