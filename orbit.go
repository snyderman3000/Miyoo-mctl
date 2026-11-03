package main

// Orbit engine: TLE parsing and SGP4 (near-Earth, WGS-72, after Vallado et al.
// "Revisiting Spacetrack Report #3", 2006), plus frames, the Sun, shadow and
// observer geometry. Everything a handheld needs, nothing it doesn't.

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

const (
	twoPi   = 2 * math.Pi
	deg     = math.Pi / 180
	reWGS72 = 6378.135
	muWGS72 = 398600.8
	j2      = 0.001082616
	j3      = -0.00000253881
	j4      = -0.00000165597
	j3oj2   = j3 / j2
	x2o3    = 2.0 / 3.0

	reWGS84 = 6378.137
	fWGS84  = 1 / 298.257223563
	rSun    = 696000.0
	auKm    = 149597870.7
)

var (
	xke      = 60 / math.Sqrt(reWGS72*reWGS72*reWGS72/muWGS72)
	vkmpersc = reWGS72 * xke / 60
)

type Vec3 [3]float64

func (a Vec3) Add(b Vec3) Vec3       { return Vec3{a[0] + b[0], a[1] + b[1], a[2] + b[2]} }
func (a Vec3) Sub(b Vec3) Vec3       { return Vec3{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func (a Vec3) Scale(k float64) Vec3  { return Vec3{a[0] * k, a[1] * k, a[2] * k} }
func (a Vec3) Dot(b Vec3) float64    { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func (a Vec3) Norm() float64         { return math.Sqrt(a.Dot(a)) }
func (a Vec3) Unit() Vec3            { return a.Scale(1 / a.Norm()) }
func (a Vec3) Cross(b Vec3) Vec3 {
	return Vec3{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}

// TLE holds a parsed two-line element set and its SGP4 initialisation.
type TLE struct {
	Name   string
	Line1  string
	Line2  string
	Catnr  int
	Intl   string // international designator, e.g. 98067A
	Epoch  time.Time
	Rev    int // revolution number at epoch
	Incl   float64 // degrees
	RAAN   float64
	Ecc    float64
	ArgP   float64
	MeanAn float64
	MeanMo float64 // rev/day
	BStar  float64

	s sgp4State
}

type sgp4State struct {
	isimp                                                  bool
	no, ao, ecco, inclo, nodeo, argpo, mo, bstar           float64
	con41, cc1, cc4, cc5, d2, d3, d4, delmo, eta, argpdot  float64
	omgcof, sinmao, t2cof, t3cof, t4cof, t5cof, x1mth2     float64
	x7thm1, mdot, nodedot, xlcof, xmcof, nodecf, aycof     float64
}

// tleChecksum verifies the modulo-10 checksum at column 69.
func tleChecksum(l string) bool {
	if len(l) < 69 {
		return false
	}
	sum := 0
	for _, c := range l[:68] {
		switch {
		case c >= '0' && c <= '9':
			sum += int(c - '0')
		case c == '-':
			sum++
		}
	}
	return int(l[68]-'0') == sum%10
}

func fld(l string, a, b int) string {
	if b > len(l) {
		b = len(l)
	}
	if a >= b {
		return ""
	}
	return strings.TrimSpace(l[a:b])
}

// expo parses the TLE "assumed decimal point" exponent format: " 12345-3" = 0.12345e-3.
func expo(s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	sign := 1.0
	if s[0] == '-' || s[0] == '+' {
		if s[0] == '-' {
			sign = -1
		}
		s = s[1:]
	}
	i := strings.LastIndexAny(s, "+-")
	if i <= 0 {
		v, _ := strconv.ParseFloat("0."+s, 64)
		return sign * v
	}
	m, _ := strconv.ParseFloat("0."+s[:i], 64)
	e, _ := strconv.Atoi(s[i:])
	return sign * m * math.Pow(10, float64(e))
}

// ParseTLE parses a name line (may be empty) and the two element lines.
func ParseTLE(name, l1, l2 string) (*TLE, error) {
	l1, l2 = strings.TrimRight(l1, " \r"), strings.TrimRight(l2, " \r")
	if len(l1) < 64 || len(l2) < 63 || l1[0] != '1' || l2[0] != '2' {
		return nil, errors.New("malformed TLE")
	}
	if !tleChecksum(l1) || !tleChecksum(l2) {
		return nil, errors.New("TLE checksum mismatch")
	}
	t := &TLE{Name: strings.TrimSpace(strings.TrimPrefix(name, "0 ")), Line1: l1, Line2: l2}
	var err error
	if t.Catnr, err = strconv.Atoi(fld(l1, 2, 7)); err != nil {
		return nil, fmt.Errorf("catalog number: %v", err)
	}
	t.Intl = fld(l1, 9, 17)
	yy, err := strconv.Atoi(fld(l1, 18, 20))
	if err != nil {
		return nil, fmt.Errorf("epoch year: %v", err)
	}
	doy, err := strconv.ParseFloat(fld(l1, 20, 32), 64)
	if err != nil {
		return nil, fmt.Errorf("epoch day: %v", err)
	}
	year := 1900 + yy
	if yy < 57 {
		year = 2000 + yy
	}
	t.Epoch = time.Date(year, 1, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration((doy - 1) * 86400e9))
	t.BStar = expo(fld(l1, 53, 61))
	pf := func(a, b int) float64 { v, _ := strconv.ParseFloat(fld(l2, a, b), 64); return v }
	t.Incl, t.RAAN = pf(8, 16), pf(17, 25)
	t.Ecc, _ = strconv.ParseFloat("0."+fld(l2, 26, 33), 64)
	t.ArgP, t.MeanAn, t.MeanMo = pf(34, 42), pf(43, 51), pf(52, 63)
	t.Rev, _ = strconv.Atoi(fld(l2, 63, 68))
	if t.MeanMo <= 0 {
		return nil, errors.New("bad mean motion")
	}
	if t.Name == "" {
		t.Name = strconv.Itoa(t.Catnr)
	}
	if err := t.init(); err != nil {
		return nil, err
	}
	return t, nil
}

// ParseTLEText reads 2- or 3-line element sets from a CelesTrak-style text file.
func ParseTLEText(text string) []*TLE {
	var out []*TLE
	lines := strings.Split(strings.ReplaceAll(text, "\r", ""), "\n")
	name := ""
	for i := 0; i < len(lines); i++ {
		l := lines[i]
		if strings.HasPrefix(l, "1 ") && i+1 < len(lines) && strings.HasPrefix(lines[i+1], "2 ") {
			if t, err := ParseTLE(name, l, lines[i+1]); err == nil {
				out = append(out, t)
			}
			name = ""
			i++
			continue
		}
		if strings.TrimSpace(l) != "" {
			name = l
		}
	}
	return out
}

// PeriodMin is the orbital period in minutes.
func (t *TLE) PeriodMin() float64 { return 1440 / t.MeanMo }

var errDeepSpace = errors.New("deep-space orbit (period over 225 min) not supported")

func (t *TLE) init() error {
	s := &t.s
	s.ecco, s.inclo, s.nodeo = t.Ecc, t.Incl*deg, t.RAAN*deg
	s.argpo, s.mo, s.bstar = t.ArgP*deg, t.MeanAn*deg, t.BStar
	noKozai := t.MeanMo * twoPi / 1440
	if twoPi/noKozai >= 225 {
		return errDeepSpace
	}
	// initl
	eccsq := s.ecco * s.ecco
	omeosq := 1 - eccsq
	rteosq := math.Sqrt(omeosq)
	cosio := math.Cos(s.inclo)
	cosio2 := cosio * cosio
	ak := math.Pow(xke/noKozai, x2o3)
	d1 := 0.75 * j2 * (3*cosio2 - 1) / (rteosq * omeosq)
	del := d1 / (ak * ak)
	adel := ak * (1 - del*del - del*(1.0/3+134*del*del/81))
	del = d1 / (adel * adel)
	s.no = noKozai / (1 + del)
	s.ao = math.Pow(xke/s.no, x2o3)
	sinio := math.Sin(s.inclo)
	po := s.ao * omeosq
	con42 := 1 - 5*cosio2
	s.con41 = -con42 - cosio2 - cosio2
	posq := po * po
	rp := s.ao * (1 - s.ecco)

	s.isimp = rp < 220/reWGS72+1
	sfour := 78/reWGS72 + 1
	qzms24 := math.Pow((120-78)/reWGS72, 4)
	perige := (rp - 1) * reWGS72
	if perige < 156 {
		sfour = perige - 78
		if perige < 98 {
			sfour = 20
		}
		qzms24 = math.Pow((120-sfour)/reWGS72, 4)
		sfour = sfour/reWGS72 + 1
	}
	pinvsq := 1 / posq
	tsi := 1 / (s.ao - sfour)
	s.eta = s.ao * s.ecco * tsi
	etasq := s.eta * s.eta
	eeta := s.ecco * s.eta
	psisq := math.Abs(1 - etasq)
	coef := qzms24 * math.Pow(tsi, 4)
	coef1 := coef / math.Pow(psisq, 3.5)
	cc2 := coef1 * s.no * (s.ao*(1+1.5*etasq+eeta*(4+etasq)) +
		0.375*j2*tsi/psisq*s.con41*(8+3*etasq*(8+etasq)))
	s.cc1 = s.bstar * cc2
	cc3 := 0.0
	if s.ecco > 1e-4 {
		cc3 = -2 * coef * tsi * j3oj2 * s.no * sinio / s.ecco
	}
	s.x1mth2 = 1 - cosio2
	s.cc4 = 2 * s.no * coef1 * s.ao * omeosq * (s.eta*(2+0.5*etasq) + s.ecco*(0.5+2*etasq) -
		j2*tsi/(s.ao*psisq)*(-3*s.con41*(1-2*eeta+etasq*(1.5-0.5*eeta))+
			0.75*s.x1mth2*(2*etasq-eeta*(1+etasq))*math.Cos(2*s.argpo)))
	s.cc5 = 2 * coef1 * s.ao * omeosq * (1 + 2.75*(etasq+eeta) + eeta*etasq)
	cosio4 := cosio2 * cosio2
	temp1 := 1.5 * j2 * pinvsq * s.no
	temp2 := 0.5 * temp1 * j2 * pinvsq
	temp3 := -0.46875 * j4 * pinvsq * pinvsq * s.no
	s.mdot = s.no + 0.5*temp1*rteosq*s.con41 + 0.0625*temp2*rteosq*(13-78*cosio2+137*cosio4)
	s.argpdot = -0.5*temp1*con42 + 0.0625*temp2*(7-114*cosio2+395*cosio4) + temp3*(3-36*cosio2+49*cosio4)
	xhdot1 := -temp1 * cosio
	s.nodedot = xhdot1 + (0.5*temp2*(4-19*cosio2)+2*temp3*(3-7*cosio2))*cosio
	s.omgcof = s.bstar * cc3 * math.Cos(s.argpo)
	s.xmcof = 0
	if s.ecco > 1e-4 {
		s.xmcof = -x2o3 * coef * s.bstar / eeta
	}
	s.nodecf = 3.5 * omeosq * xhdot1 * s.cc1
	s.t2cof = 1.5 * s.cc1
	if math.Abs(cosio+1) > 1.5e-12 {
		s.xlcof = -0.25 * j3oj2 * sinio * (3 + 5*cosio) / (1 + cosio)
	} else {
		s.xlcof = -0.25 * j3oj2 * sinio * (3 + 5*cosio) / 1.5e-12
	}
	s.aycof = -0.5 * j3oj2 * sinio
	dm := 1 + s.eta*math.Cos(s.mo)
	s.delmo = dm * dm * dm
	s.sinmao = math.Sin(s.mo)
	s.x7thm1 = 7*cosio2 - 1
	if !s.isimp {
		cc1sq := s.cc1 * s.cc1
		s.d2 = 4 * s.ao * tsi * cc1sq
		temp := s.d2 * tsi * s.cc1 / 3
		s.d3 = (17*s.ao + sfour) * temp
		s.d4 = 0.5 * temp * s.ao * tsi * (221*s.ao + 31*sfour) * s.cc1
		s.t3cof = s.d2 + 2*cc1sq
		s.t4cof = 0.25 * (3*s.d3 + s.cc1*(12*s.d2+10*cc1sq))
		s.t5cof = 0.2 * (3*s.d4 + 12*s.cc1*s.d3 + 6*s.d2*s.d2 + 15*cc1sq*(2*s.d2+cc1sq))
	}
	return nil
}

var errDecayed = errors.New("satellite has decayed")

// Propagate returns TEME position (km) and velocity (km/s) tsince minutes from epoch.
func (t *TLE) Propagate(tsince float64) (Vec3, Vec3, error) {
	s := &t.s
	xmdf := s.mo + s.mdot*tsince
	argpdf := s.argpo + s.argpdot*tsince
	nodedf := s.nodeo + s.nodedot*tsince
	argpm, mm := argpdf, xmdf
	t2 := tsince * tsince
	nodem := nodedf + s.nodecf*t2
	tempa := 1 - s.cc1*tsince
	tempe := s.bstar * s.cc4 * tsince
	templ := s.t2cof * t2
	if !s.isimp {
		delomg := s.omgcof * tsince
		dmt := 1 + s.eta*math.Cos(xmdf)
		delm := s.xmcof * (dmt*dmt*dmt - s.delmo)
		temp := delomg + delm
		mm = xmdf + temp
		argpm = argpdf - temp
		t3 := t2 * tsince
		t4 := t3 * tsince
		tempa = tempa - s.d2*t2 - s.d3*t3 - s.d4*t4
		tempe = tempe + s.bstar*s.cc5*(math.Sin(mm)-s.sinmao)
		templ = templ + s.t3cof*t3 + t4*(s.t4cof+tsince*s.t5cof)
	}
	am := math.Pow(xke/s.no, x2o3) * tempa * tempa
	nm := xke / math.Pow(am, 1.5)
	em := s.ecco - tempe
	if em >= 1 || em < -0.001 || am < 0.95 {
		return Vec3{}, Vec3{}, errDecayed
	}
	if em < 1e-6 {
		em = 1e-6
	}
	mm += s.no * templ
	xlm := mm + argpm + nodem
	nodem = math.Mod(nodem, twoPi)
	argpm = math.Mod(argpm, twoPi)
	xlm = math.Mod(xlm, twoPi)
	mm = math.Mod(xlm-argpm-nodem, twoPi)

	sinip, cosip := math.Sin(s.inclo), math.Cos(s.inclo)
	axnl := em * math.Cos(argpm)
	temp := 1 / (am * (1 - em*em))
	aynl := em*math.Sin(argpm) + temp*s.aycof
	xl := mm + argpm + nodem + temp*s.xlcof*axnl

	u := math.Mod(xl-nodem, twoPi)
	eo1 := u
	tem5 := 9999.9
	var sineo1, coseo1 float64
	for ktr := 1; math.Abs(tem5) >= 1e-12 && ktr <= 10; ktr++ {
		sineo1, coseo1 = math.Sin(eo1), math.Cos(eo1)
		tem5 = 1 - coseo1*axnl - sineo1*aynl
		tem5 = (u - aynl*coseo1 + axnl*sineo1 - eo1) / tem5
		if math.Abs(tem5) >= 0.95 {
			tem5 = math.Copysign(0.95, tem5)
		}
		eo1 += tem5
	}
	ecose := axnl*coseo1 + aynl*sineo1
	esine := axnl*sineo1 - aynl*coseo1
	el2 := axnl*axnl + aynl*aynl
	pl := am * (1 - el2)
	if pl < 0 {
		return Vec3{}, Vec3{}, errDecayed
	}
	rl := am * (1 - ecose)
	rdotl := math.Sqrt(am) * esine / rl
	rvdotl := math.Sqrt(pl) / rl
	betal := math.Sqrt(1 - el2)
	temp = esine / (1 + betal)
	sinu := am / rl * (sineo1 - aynl - axnl*temp)
	cosu := am / rl * (coseo1 - axnl + aynl*temp)
	su := math.Atan2(sinu, cosu)
	sin2u := (cosu + cosu) * sinu
	cos2u := 1 - 2*sinu*sinu
	temp = 1 / pl
	temp1 := 0.5 * j2 * temp
	temp2 := temp1 * temp
	mrt := rl*(1-1.5*temp2*betal*s.con41) + 0.5*temp1*s.x1mth2*cos2u
	su -= 0.25 * temp2 * s.x7thm1 * sin2u
	xnode := nodem + 1.5*temp2*cosip*sin2u
	xinc := s.inclo + 1.5*temp2*cosip*sinip*cos2u
	mvt := rdotl - nm*temp1*s.x1mth2*sin2u/xke
	rvdot := rvdotl + nm*temp1*(s.x1mth2*cos2u+1.5*s.con41)/xke
	sinsu, cossu := math.Sin(su), math.Cos(su)
	snod, cnod := math.Sin(xnode), math.Cos(xnode)
	sini, cosi := math.Sin(xinc), math.Cos(xinc)
	xmx, xmy := -snod*cosi, cnod*cosi
	ux, uy, uz := xmx*sinsu+cnod*cossu, xmy*sinsu+snod*cossu, sini*sinsu
	vx, vy, vz := xmx*cossu-cnod*sinsu, xmy*cossu-snod*sinsu, sini*cossu
	r := Vec3{mrt * ux * reWGS72, mrt * uy * reWGS72, mrt * uz * reWGS72}
	v := Vec3{(mvt*ux + rvdot*vx) * vkmpersc, (mvt*uy + rvdot*vy) * vkmpersc, (mvt*uz + rvdot*vz) * vkmpersc}
	if mrt < 1 {
		return r, v, errDecayed
	}
	return r, v, nil
}

// At propagates to an absolute time.
func (t *TLE) At(when time.Time) (Vec3, Vec3, error) {
	return t.Propagate(when.Sub(t.Epoch).Minutes())
}

// ---------- time and frames ----------

func julian(t time.Time) float64 {
	return float64(t.UnixNano())/86400e9 + 2440587.5
}

// GMST (IAU-82, radians) — the same sidereal time SGP4's TEME frame expects.
func gmst(t time.Time) float64 {
	tut1 := (julian(t) - 2451545) / 36525
	sec := -6.2e-6*tut1*tut1*tut1 + 0.093104*tut1*tut1 + (876600*3600+8640184.812866)*tut1 + 67310.54841
	g := math.Mod(sec*deg/240, twoPi)
	if g < 0 {
		g += twoPi
	}
	return g
}

// temeToECEF rotates an inertial vector into the Earth-fixed frame.
func temeToECEF(r Vec3, t time.Time) Vec3 {
	g := gmst(t)
	c, s := math.Cos(g), math.Sin(g)
	return Vec3{c*r[0] + s*r[1], -s*r[0] + c*r[1], r[2]}
}

// Geo is a geodetic position: degrees, degrees, km above the WGS-84 ellipsoid.
type Geo struct{ Lat, Lon, Alt float64 }

func ecefToGeo(r Vec3) Geo {
	e2 := fWGS84 * (2 - fWGS84)
	p := math.Hypot(r[0], r[1])
	lon := math.Atan2(r[1], r[0])
	lat := math.Atan2(r[2], p*(1-e2))
	var n float64
	for i := 0; i < 5; i++ {
		sl := math.Sin(lat)
		n = reWGS84 / math.Sqrt(1-e2*sl*sl)
		lat = math.Atan2(r[2]+e2*n*sl, p)
	}
	sl := math.Sin(lat)
	n = reWGS84 / math.Sqrt(1-e2*sl*sl)
	var alt float64
	if math.Abs(math.Cos(lat)) > 1e-6 {
		alt = p/math.Cos(lat) - n
	} else {
		alt = math.Abs(r[2]) - n*(1-e2)
	}
	return Geo{lat / deg, lon / deg, alt}
}

func geoToECEF(g Geo) Vec3 {
	e2 := fWGS84 * (2 - fWGS84)
	la, lo := g.Lat*deg, g.Lon*deg
	sl := math.Sin(la)
	n := reWGS84 / math.Sqrt(1-e2*sl*sl)
	return Vec3{(n + g.Alt) * math.Cos(la) * math.Cos(lo), (n + g.Alt) * math.Cos(la) * math.Sin(lo), (n*(1-e2) + g.Alt) * sl}
}

// ---------- the Sun ----------

// sunECI is the Sun's position (km, mean equator of date — near enough to TEME).
func sunECI(t time.Time) Vec3 {
	n := julian(t) - 2451545
	L := math.Mod(280.460+0.9856474*n, 360) * deg
	g := math.Mod(357.528+0.9856003*n, 360) * deg
	lam := L + (1.915*math.Sin(g)+0.020*math.Sin(2*g))*deg
	eps := (23.439 - 0.0000004*n) * deg
	R := (1.00014 - 0.01671*math.Cos(g) - 0.00014*math.Cos(2*g)) * auKm
	return Vec3{R * math.Cos(lam), R * math.Cos(eps) * math.Sin(lam), R * math.Sin(eps) * math.Sin(lam)}
}

// subSolar is the point on Earth with the Sun overhead.
func subSolar(t time.Time) Geo {
	s := temeToECEF(sunECI(t), t)
	return Geo{math.Atan2(s[2], math.Hypot(s[0], s[1])) / deg, math.Atan2(s[1], s[0]) / deg, 0}
}

// Shadow states.
const (
	Sunlit = iota
	Penumbra
	Umbra
)

// shadow uses the conical model: compares the Earth's and Sun's apparent disks from the satellite.
func shadow(r Vec3, t time.Time) int {
	sun := sunECI(t)
	toSun := sun.Sub(r)
	dr, ds := r.Norm(), toSun.Norm()
	if dr <= reWGS84 {
		return Umbra
	}
	a := math.Asin(reWGS84 / dr)
	b := math.Asin(rSun / ds)
	c := math.Acos(clamp(r.Scale(-1).Dot(toSun)/(dr*ds), -1, 1))
	switch {
	case c < a-b:
		return Umbra
	case c < a+b:
		return Penumbra
	}
	return Sunlit
}

// betaAngle is the Sun's elevation above the orbit plane, degrees.
func betaAngle(r, v Vec3, t time.Time) float64 {
	h := r.Cross(v).Unit()
	return math.Asin(clamp(h.Dot(sunECI(t).Unit()), -1, 1)) / deg
}

func clamp(x, a, b float64) float64 { return math.Max(a, math.Min(b, x)) }

// ---------- observer ----------

// Look is the view of a target from a ground site.
type Look struct {
	Az, El, Range, RangeRate float64 // deg, deg, km, km/s
}

func lookAngles(site Geo, rECEF, vECEF Vec3) Look {
	o := geoToECEF(site)
	d := rECEF.Sub(o)
	la, lo := site.Lat*deg, site.Lon*deg
	sla, cla, slo, clo := math.Sin(la), math.Cos(la), math.Sin(lo), math.Cos(lo)
	e := -slo*d[0] + clo*d[1]
	n := -sla*clo*d[0] - sla*slo*d[1] + cla*d[2]
	u := cla*clo*d[0] + cla*slo*d[1] + sla*d[2]
	rng := d.Norm()
	az := math.Atan2(e, n) / deg
	if az < 0 {
		az += 360
	}
	return Look{Az: az, El: math.Asin(u/rng) / deg, Range: rng, RangeRate: d.Dot(vECEF) / rng}
}

// sunElevation at a ground site, degrees.
func sunElevation(site Geo, t time.Time) float64 {
	return lookAngles(site, temeToECEF(sunECI(t), t), Vec3{}).El
}

// ecefVelocity converts a TEME velocity to Earth-fixed (accounting for Earth rotation).
func ecefVelocity(r, v Vec3, t time.Time) Vec3 {
	const omega = 7.292115146706979e-5
	re := temeToECEF(r, t)
	ve := temeToECEF(v, t)
	return Vec3{ve[0] + omega*re[1], ve[1] - omega*re[0], ve[2]}
}

// State is everything the screens show about a vehicle at one instant.
type State struct {
	T          time.Time
	R, V       Vec3 // TEME
	Geo        Geo
	Speed      float64 // km/s inertial
	Shadow     int
	Beta       float64
	Look       Look
	Err        error
}

func (t *TLE) StateAt(when time.Time, site *Geo) State {
	r, v, err := t.At(when)
	st := State{T: when, R: r, V: v, Err: err}
	if err != nil && err != errDecayed {
		return st
	}
	re := temeToECEF(r, when)
	st.Geo = ecefToGeo(re)
	st.Speed = v.Norm()
	st.Shadow = shadow(r, when)
	st.Beta = betaAngle(r, v, when)
	if site != nil {
		st.Look = lookAngles(*site, re, ecefVelocity(r, v, when))
	}
	return st
}

// Elements derived from the TLE for display.
func (t *TLE) ApsidesKm() (apo, peri float64) {
	a := t.s.ao * reWGS72
	return a*(1+t.Ecc) - reWGS72, a*(1-t.Ecc) - reWGS72
}

// orbitNumber is the revolution count at time when (rev counts up at each ascending node).
func (t *TLE) orbitNumber(when time.Time) int {
	revs := when.Sub(t.Epoch).Minutes() / t.PeriodMin()
	// the TLE rev number refers to the last ascending node before epoch; mean anomaly+argp approximates position in orbit
	frac := math.Mod(t.ArgP+t.MeanAn, 360) / 360
	return t.Rev + int(math.Floor(frac+revs))
}
