package main

import (
	"fmt"
	"image/color"
	"log"
	"math"
	"os"
	"strings"
	"time"
)

const (
	pgTrack = iota
	pgMaster
	pgPass
	pgSys
	nPages
)

var pageNames = []string{"TRACK", "MASTER", "PASS", "SYS"}

const (
	modalNone = iota
	modalSites
	modalEdit
	modalHelp
)

var warpSteps = []float64{-3600, -600, -60, -10, 1, 10, 60, 600, 3600}

type UI struct {
	env        *Env
	W, H       int
	fL, fM, fS, fXS *Font
	th         Theme
	themeIdx   int
	page       int
	modal      int
	quit       bool
	frame      int
	post       chan func()

	// clock
	warpIdx              int
	anchorReal, anchorSim time.Time

	// data
	fleet    []*Vehicle
	sel      int
	meta     Meta
	fetching bool
	netErr   string
	lastTry  time.Time

	// passes
	passes      []Pass
	passKey     string
	passFrom    time.Time
	passBusy    bool
	passSel     int
	passScroll  int
	visOnly     bool

	// map caches
	mapBase      *Canvas
	mapNight     *Canvas
	mapSun       Geo
	mapTheme     int
	mapFresh     bool
	allOnMap     bool

	// globe
	follow       bool
	viewLat      float64
	viewLon      float64
	exaggerate   bool
	globeImg     *Canvas
	globeKey     string

	// sys + modals
	sysSel     int
	siteSel    int
	editSite   Site
	editField  int

	kiosk   bool
	kioskAt time.Time
	toast   string
	toastAt time.Time
}

func NewUI(env *Env, w, h int) *UI {
	u := &UI{env: env, W: w, H: h, post: make(chan func(), 16), follow: true, exaggerate: true, mapTheme: -1, allOnMap: true}
	u.fL = loadFont(vt323TTF, 40)
	u.fM = loadFont(vt323TTF, 24)
	u.fS = loadFont(vt323TTF, 20)
	u.fXS = loadFont(vt323TTF, 17)
	u.themeIdx = themeIndex(env.Config.Theme)
	u.th = themes[u.themeIdx]
	u.warpIdx = 4
	u.goLive()
	return u
}

// ---------- clock ----------

func (u *UI) warp() float64 { return warpSteps[u.warpIdx] }

func (u *UI) now() time.Time {
	real := u.env.RealNow()
	return u.anchorSim.Add(time.Duration(float64(real.Sub(u.anchorReal)) * u.warp()))
}

func (u *UI) setWarp(i int) {
	t := u.now()
	u.anchorReal, u.anchorSim = u.env.RealNow(), t
	u.warpIdx = i
}

func (u *UI) goLive() {
	r := u.env.RealNow()
	u.anchorReal, u.anchorSim, u.warpIdx = r, r, 4
}

func (u *UI) isLive() bool {
	return u.warpIdx == 4 && math.Abs(float64(u.now().Sub(u.env.RealNow()))) < float64(2*time.Second)
}

// ---------- lifecycle ----------

func (u *UI) site() *Site { return u.env.Config.Site }

func (u *UI) cur() *Vehicle {
	if len(u.fleet) == 0 {
		return nil
	}
	return u.fleet[u.sel]
}

func (u *UI) setFleet(tles []*TLE) {
	keep := u.env.Config.Vehicle
	if v := u.cur(); v != nil {
		keep = v.TLE.Catnr
	}
	u.fleet = buildFleet(tles, u.env.Config.Extra, u.env.RealNow())
	u.sel = 0
	for i, v := range u.fleet {
		if v.TLE.Catnr == keep {
			u.sel = i
		}
	}
	u.passKey = ""
}

// Start loads the cache and kicks off a download if the elements are old.
func (u *UI) Start() {
	tles, m := u.env.Store.Load()
	u.meta = m
	u.setFleet(tles)
	if u.site() == nil {
		u.modal = modalSites
	}
	if time.Since(m.Fetched) > fetchEvery || len(u.fleet) == 0 {
		u.refresh(false)
	}
}

func (u *UI) refresh(manual bool) {
	if u.fetching || u.env.Store == nil || u.env.Demo {
		return
	}
	if manual && time.Since(u.meta.Fetched) < fetchMinGap {
		u.say("ELEMENTS ARE FRESH — CELESTRAK ASKS FOR <1 PULL PER 2H")
		return
	}
	u.fetching = true
	u.lastTry = time.Now()
	extra := u.env.Config.Extra
	go func() {
		tles, m, err := u.env.Store.Fetch(extra)
		for try := 0; err != nil && shortErr(err) == "NO NETWORK" && try < 6; try++ {
			time.Sleep(5 * time.Second) // Wi-Fi may still be joining right after boot
			tles, m, err = u.env.Store.Fetch(extra)
		}
		u.post <- func() {
			u.fetching = false
			if err != nil {
				u.netErr = shortErr(err)
				log.Printf("fetch: %v", err)
				if len(u.fleet) > 0 {
					u.say("OFFLINE — USING CACHED ELEMENTS")
				}
				return
			}
			u.netErr = ""
			u.meta = m
			if off, ok := u.env.Store.ClockOffset(); ok {
				u.env.ClockOffset = off
				if u.isLiveish() {
					u.goLive()
				}
			}
			u.setFleet(tles)
			u.say(fmt.Sprintf("ELEMENTS UPDATED — %d VEHICLES", len(u.fleet)))
		}
	}()
}

func (u *UI) isLiveish() bool { return u.warpIdx == 4 }

func shortErr(err error) string {
	s := strings.ToUpper(err.Error())
	switch {
	case strings.Contains(s, "NO SUCH HOST"), strings.Contains(s, "NETWORK IS UNREACHABLE"), strings.Contains(s, "DIAL"):
		return "NO NETWORK"
	case strings.Contains(s, "TIMEOUT"):
		return "TIMED OUT"
	case strings.Contains(s, "CERTIFICATE"):
		return "TLS ERROR (CLOCK?)"
	}
	if len(s) > 40 {
		s = s[:40]
	}
	return s
}

func (u *UI) say(s string) { u.toast, u.toastAt = s, time.Now() }

// Tick runs housekeeping between frames.
func (u *UI) Tick() {
	now := u.now()
	if u.kiosk && time.Since(u.kioskAt) > time.Duration(u.env.Config.Kiosk)*time.Second {
		u.kioskAt = time.Now()
		u.page = (u.page + 1) % pgSys
	}
	u.ensurePasses(now)
	if !u.fetching && !u.env.Demo && time.Since(u.meta.Fetched) > fetchEvery && time.Since(u.lastTry) > 15*time.Minute {
		u.refresh(false)
	}
}

func (u *UI) ensurePasses(now time.Time) {
	v, s := u.cur(), u.site()
	if v == nil || s == nil || u.passBusy {
		return
	}
	key := fmt.Sprintf("%d|%s|%.3f|%.3f", v.TLE.Catnr, v.TLE.Epoch, s.Lat, s.Lon)
	stale := key != u.passKey || now.Before(u.passFrom.Add(-time.Minute)) ||
		now.After(u.passFrom.Add(3*24*time.Hour))
	if !stale {
		// drop finished passes; refill when few remain
		live := 0
		for _, p := range u.passes {
			if p.LOS.After(now) {
				live++
			}
		}
		stale = live < 4
	}
	if !stale {
		return
	}
	u.passBusy = true
	t, g, from := v.TLE, s.Geo(), now.Add(-20*time.Minute)
	horizon := 5 * 24 * time.Hour
	if u.env.Sync {
		u.passes, u.passKey, u.passFrom, u.passBusy = PredictPasses(t, g, from, horizon, 40), key, from, false
		return
	}
	go func() {
		ps := PredictPasses(t, g, from, horizon, 40)
		u.post <- func() {
			u.passes, u.passKey, u.passFrom, u.passBusy = ps, key, from, false
			u.passSel, u.passScroll = 0, 0
		}
	}()
}

// listPasses is what the PASS page shows: upcoming (or in progress), optionally visible only.
func (u *UI) listPasses(now time.Time) []Pass {
	var out []Pass
	for _, p := range u.passes {
		if p.LOS.Before(now) {
			continue
		}
		if u.visOnly && !p.Visible {
			continue
		}
		out = append(out, p)
	}
	return out
}

func (u *UI) Animating() bool { return true }

// ---------- input ----------

func (u *UI) Key(k int) {
	if u.kiosk && k != BtnStart && k != BtnMenu {
		u.kiosk = false
		u.env.StayAwake(false)
		u.say("KIOSK OFF")
	}
	switch u.modal {
	case modalSites:
		u.keySites(k)
		return
	case modalEdit:
		u.keyEdit(k)
		return
	case modalHelp:
		u.modal = modalNone
		return
	}
	switch k {
	case BtnMenu:
		u.quit = true
	case BtnL1:
		u.page = (u.page + nPages - 1) % nPages
	case BtnR1:
		u.page = (u.page + 1) % nPages
	case BtnX:
		if len(u.fleet) > 0 {
			u.sel = (u.sel + 1) % len(u.fleet)
			u.env.Config.Vehicle = u.fleet[u.sel].TLE.Catnr
			u.env.SaveConfig()
			u.passSel, u.passScroll, u.follow = 0, 0, true
		}
	case BtnSelect:
		u.cycleTheme()
	case BtnL2:
		if u.warpIdx > 0 {
			u.setWarp(u.warpIdx - 1)
		}
	case BtnR2:
		if u.warpIdx < len(warpSteps)-1 {
			u.setWarp(u.warpIdx + 1)
		}
	case BtnY:
		u.goLive()
		u.say("LIVE")
	case BtnStart:
		u.kiosk = !u.kiosk
		u.kioskAt = time.Now()
		u.env.StayAwake(u.kiosk)
		if u.kiosk {
			u.say(fmt.Sprintf("KIOSK — CYCLING PAGES EVERY %dS, SCREEN STAYS ON", u.env.Config.Kiosk))
		} else {
			u.say("KIOSK OFF")
		}
	default:
		switch u.page {
		case pgTrack:
			if k == BtnA {
				u.allOnMap = !u.allOnMap
			} else if k == BtnB {
				u.modal = modalHelp
			}
		case pgMaster:
			u.keyMaster(k)
		case pgPass:
			u.keyPass(k)
		case pgSys:
			u.keySys(k)
		}
	}
}

func (u *UI) cycleTheme() {
	u.themeIdx = (u.themeIdx + 1) % len(themes)
	u.th = themes[u.themeIdx]
	u.env.Config.Theme = u.th.Name
	u.env.SaveConfig()
	u.say("THEME: " + strings.ToUpper(u.th.Name))
}

func (u *UI) keyMaster(k int) {
	step := 10.0
	switch k {
	case BtnA:
		u.follow = !u.follow
		return
	case BtnB:
		u.exaggerate = !u.exaggerate
		return
	case BtnUp:
		u.viewLat = clamp(u.viewLat+step, -80, 80)
	case BtnDown:
		u.viewLat = clamp(u.viewLat-step, -80, 80)
	case BtnLeft:
		u.viewLon = normLon(u.viewLon - step)
	case BtnRight:
		u.viewLon = normLon(u.viewLon + step)
	default:
		return
	}
	u.follow = false
}

func (u *UI) keyPass(k int) {
	n := len(u.listPasses(u.now()))
	switch k {
	case BtnUp:
		if u.passSel > 0 {
			u.passSel--
		}
	case BtnDown:
		if u.passSel < n-1 {
			u.passSel++
		}
	case BtnLeft:
		u.passSel = max(0, u.passSel-8)
	case BtnRight:
		u.passSel = max(0, min(n-1, u.passSel+8))
	case BtnA:
		u.visOnly = !u.visOnly
		u.passSel, u.passScroll = 0, 0
	}
}

const (
	sysSite = iota
	sysTheme
	sysScan
	sysKiosk
	sysRefresh
	sysHelp
	nSys
)

func (u *UI) keySys(k int) {
	switch k {
	case BtnUp:
		u.sysSel = (u.sysSel + nSys - 1) % nSys
	case BtnDown:
		u.sysSel = (u.sysSel + 1) % nSys
	case BtnA, BtnRight, BtnLeft:
		switch u.sysSel {
		case sysSite:
			u.modal = modalSites
		case sysTheme:
			u.cycleTheme()
		case sysScan:
			u.env.Config.Scanlines = !u.env.Config.Scanlines
			u.env.SaveConfig()
		case sysKiosk:
			opts := []int{10, 20, 30, 60, 120}
			i := 0
			for j, o := range opts {
				if o == u.env.Config.Kiosk {
					i = j
				}
			}
			if k == BtnLeft {
				i = (i + len(opts) - 1) % len(opts)
			} else {
				i = (i + 1) % len(opts)
			}
			u.env.Config.Kiosk = opts[i]
			u.env.SaveConfig()
		case sysRefresh:
			u.refresh(true)
		case sysHelp:
			u.modal = modalHelp
		}
	}
}

func (u *UI) keySites(k int) {
	n := len(presetSites) + 1 // row 0 is "custom"
	switch k {
	case BtnUp:
		u.siteSel = (u.siteSel + n - 1) % n
	case BtnDown:
		u.siteSel = (u.siteSel + 1) % n
	case BtnL1, BtnLeft:
		u.siteSel = max(0, u.siteSel-10)
	case BtnR1, BtnRight:
		u.siteSel = min(n-1, u.siteSel+10)
	case BtnA:
		if u.siteSel == 0 {
			u.editSite = Site{Name: "Custom", Lat: 35.0, Lon: -90.0, TZ: "UTC"}
			if s := u.site(); s != nil {
				u.editSite = *s
				u.editSite.Name = "Custom"
			}
			u.editField = 0
			u.modal = modalEdit
			return
		}
		s := presetSites[u.siteSel-1]
		u.setSite(s)
	case BtnB, BtnMenu:
		if u.site() != nil {
			u.modal = modalNone
		} else if k == BtnMenu {
			u.quit = true
		}
	}
}

func (u *UI) setSite(s Site) {
	u.env.Config.Site = &s
	u.env.SaveConfig()
	u.modal = modalNone
	u.passKey = ""
	u.passes = nil
	u.say("SITE: " + strings.ToUpper(s.Name))
}

func (u *UI) keyEdit(k int) {
	f := &u.editSite.Lat
	lim := 90.0
	if u.editField == 1 {
		f, lim = &u.editSite.Lon, 180
	}
	switch k {
	case BtnX:
		u.editField ^= 1
	case BtnUp:
		*f += 0.1
	case BtnDown:
		*f -= 0.1
	case BtnRight:
		*f += 1
	case BtnLeft:
		*f -= 1
	case BtnR1:
		*f += 10
	case BtnL1:
		*f -= 10
	case BtnA:
		u.setSite(u.editSite)
	case BtnB:
		u.modal = modalSites
	}
	*f = math.Round(clamp(*f, -lim, lim)*1000) / 1000
}

// ---------- drawing ----------

const headH, footH = 30, 26

func (u *UI) Draw(c *Canvas) {
	th := u.th
	c.Fill(0, 0, c.W, c.H, th.BG)
	now := u.now()
	switch {
	case u.modal == modalSites:
		u.drawSites(c)
	case u.modal == modalEdit:
		u.drawEdit(c)
	case u.cur() == nil:
		u.drawNoData(c)
	default:
		switch u.page {
		case pgTrack:
			u.drawTrack(c, now)
		case pgMaster:
			u.drawMaster(c, now)
		case pgPass:
			u.drawPass(c, now)
		case pgSys:
			u.drawSys(c, now)
		}
	}
	if u.modal == modalHelp {
		u.drawHelp(c)
	}
	if u.modal != modalSites && u.modal != modalEdit {
		u.drawHeader(c, now)
		u.drawFooter(c)
	}
	if u.toast != "" && time.Since(u.toastAt) < 3*time.Second {
		w := u.fS.Width(u.toast) + 24
		x, y := (c.W-w)/2, c.H-footH-36
		c.Fill(x, y, w, 28, th.SelBG)
		c.TextCenter(u.fS, c.W/2, y+4, u.toast, th.SelFG)
	}
	if u.env.Config.Scanlines && th.Scan > 0 {
		c.Scanlines(th.Scan)
	}
}

func (u *UI) glow(c *Canvas, f *Font, x, y int, s string, col color.RGBA) int {
	if u.th.Glow {
		return c.TextGlow(f, x, y, s, col, u.th.BG)
	}
	return c.Text(f, x, y, s, col)
}

func (u *UI) drawHeader(c *Canvas, now time.Time) {
	th := u.th
	c.Fill(0, 0, c.W, headH, th.Panel)
	c.Fill(0, headH-1, c.W, 1, th.Faint)
	f := u.fM
	y := (headH - f.height) / 2
	x := u.glow(c, f, 8, y, "MCTL", th.Hi)
	x += 10
	if v := u.cur(); v != nil {
		name := v.Short
		if len(u.fleet) > 1 {
			name = fmt.Sprintf("%s", name)
		}
		x = c.Text(f, x, y, "▶", th.Dim) + 4
		x = c.Text(f, x, y, f.Ellipsize(name, 150), th.FG)
		if len(u.fleet) > 1 {
			x = c.Text(u.fS, x+6, y+2, fmt.Sprintf("%d/%d", u.sel+1, len(u.fleet)), th.Faint)
		}
	}
	// page tabs
	tx := 292
	for i, n := range []string{"TRK", "MST", "PAS", "SYS"} {
		w := f.Width(n) + 12
		if i == u.page {
			c.Fill(tx, 4, w, headH-8, th.SelBG)
			c.Text(f, tx+6, y, n, th.SelFG)
		} else {
			c.Text(f, tx+6, y, n, th.Dim)
		}
		tx += w + 2
	}
	clk := now.UTC().Format("15:04:05") + "Z"
	col := th.FG
	if !u.isLive() {
		col = th.Warn
	}
	u.glow(c, f, c.W-8-f.Width(clk), y, clk, col)
}

func (u *UI) drawFooter(c *Canvas) {
	th := u.th
	y0 := c.H - footH
	c.Fill(0, y0, c.W, footH, th.Panel)
	c.Fill(0, y0, c.W, 1, th.Faint)
	f := u.fS
	y := y0 + (footH-f.height)/2
	hints := "L1/R1 PAGE  X VEHICLE  L2/R2 WARP  SEL THEME"
	switch u.page {
	case pgTrack:
		hints = "L1/R1 PAGE  X VEHICLE  A OTHERS  L2/R2 WARP  B HELP"
	case pgMaster:
		hints = "L1/R1 PAGE  X VEHICLE  D-PAD ROTATE  A FOLLOW  B ALT"
	case pgPass:
		hints = "L1/R1 PAGE  X VEHICLE  ↑↓ PASS  A VISIBLE ONLY"
	case pgSys:
		hints = "L1/R1 PAGE  ↑↓ SELECT  A CHANGE  MENU QUIT"
	}
		c.Text(f, 8, y, hints, th.Dim)
	var st string
	col := th.FG
	switch {
	case u.kiosk:
		st, col = "KIOSK", th.Hi
	case !u.isLive():
		w := u.warp()
		st = fmt.Sprintf("SIM ×%g", w)
		if u.warpIdx == 4 {
			st = "SIM ×1 Y=LIVE"
		}
		col = th.Warn
	case u.fetching:
		st = "SYNC" + strings.Repeat(".", u.frame/4%4)
	case u.netErr != "":
		st, col = "OFFLINE", th.Warn
	default:
		st = "● LIVE"
		if u.frame/10%2 == 1 {
			st = "  LIVE"
		}
	}
	c.TextRight(f, c.W-8, y, st, col)
}

func (u *UI) drawNoData(c *Canvas) {
	th := u.th
	cy := c.H/2 - 60
	u.glow(c, u.fL, (c.W-u.fL.Width("NO DATA"))/2, cy, "NO DATA", th.Warn)
	lines := []string{"No orbital elements on the SD card yet.", "", "Turn on Wi-Fi, then press Y to retry."}
	if u.fetching {
		lines = []string{"Downloading elements from CelesTrak…"}
	} else if u.netErr != "" {
		lines = append([]string{"Last try: " + u.netErr}, lines...)
	}
	for i, l := range lines {
		c.TextCenter(u.fM, c.W/2, cy+60+i*26, l, th.Dim)
	}
}

// ---------- TRACK ----------

func (u *UI) mapView() MapView { return MapView{0, headH, u.W, u.W / 2} }

func (u *UI) ensureMap(now time.Time) {
	m := u.mapView()
	if u.mapBase == nil || u.mapTheme != u.themeIdx || u.mapBase.W != m.W {
		u.mapBase = baseMap(u.th, m.W, m.H)
		u.mapNight = NewCanvas(m.W, m.H)
		u.mapTheme = u.themeIdx
		u.mapFresh = false
	}
	sun := subSolar(now)
	if !u.mapFresh || math.Abs(sun.Lon-u.mapSun.Lon) > 0.25 || math.Abs(sun.Lat-u.mapSun.Lat) > 0.25 {
		nightMap(u.th, u.mapBase, sun, u.mapNight)
		u.mapSun, u.mapFresh = sun, true
	}
}

func (u *UI) drawTrack(c *Canvas, now time.Time) {
	th := u.th
	v := u.cur()
	m := u.mapView()
	u.ensureMap(now)
	c.Blit(u.mapNight, m.X, m.Y)

	// sun
	sx, sy := m.XY(u.mapSun.Lat, u.mapSun.Lon)
	c.sunIcon(int(sx), int(sy), th.Sun)

	// other vehicles
	if u.allOnMap {
		for i, o := range u.fleet {
			if i == u.sel || o.Host != "" {
				continue
			}
			st := o.TLE.StateAt(now, nil)
			if st.Err != nil {
				continue
			}
			x, y := m.XY(st.Geo.Lat, st.Geo.Lon)
			c.Fill(int(x)-2, int(y)-2, 5, 5, th.Dim)
			lx := int(x) + 5
			if lx+u.fS.Width(o.Short) > m.X+m.W-2 {
				lx = int(x) - 5 - u.fS.Width(o.Short)
			}
			c.Text(u.fS, lx, int(y)-u.fS.height/2, o.Short, th.Dim)
		}
	}

	st := v.TLE.StateAt(now, u.siteGeo())
	per := time.Duration(v.TLE.PeriodMin() * float64(time.Minute))
	past := GroundTrack(v.TLE, now.Add(-per/2), now, 30*time.Second)
	fut := GroundTrack(v.TLE, now, now.Add(per*3/2), 30*time.Second)
	c.polylineMap(m, ll(past), th.Past, 0)
	// future: solid in sunlight, dashed in shadow
	for i := 1; i < len(fut); i++ {
		a, b := fut[i-1], fut[i]
		if math.Abs(a.Lon-b.Lon) > 180 {
			continue
		}
		x0, y0 := m.XY(a.Lat, a.Lon)
		x1, y1 := m.XY(b.Lat, b.Lon)
		if b.Lit {
			c.ThickLine(int(x0), int(y0), int(x1), int(y1), th.Track)
		} else {
			c.Line(int(x0), int(y0), int(x1), int(y1), th.Track, 3)
		}
	}
	// footprint
	if st.Err == nil {
		c.polylineMap(m, footprint(st.Geo, 120), th.FG, 2)
	}
	// observer
	if s := u.site(); s != nil {
		x, y := m.XY(s.Lat, s.Lon)
		c.siteMark(int(x), int(y), th.Site)
	}
	// vehicle
	if st.Err == nil {
		x, y := m.XY(st.Geo.Lat, st.Geo.Lon)
		col := th.Hi
		if u.frame/5%2 == 1 {
			col = th.FG
		}
		c.crosshair(int(x), int(y), col, th.BG, true)
		lbl := v.Short
		lx := int(x) + 14
		if lx+u.fS.Width(lbl)+6 > m.X+m.W {
			lx = int(x) - 14 - u.fS.Width(lbl) - 6
		}
		ly := int(y) - 26
		if ly < m.Y+2 {
			ly = int(y) + 12
		}
		c.Fill(lx, ly, u.fS.Width(lbl)+6, u.fS.height, th.BG)
		c.Border(lx, ly, u.fS.Width(lbl)+6, u.fS.height, 1, th.Dim)
		c.Text(u.fS, lx+3, ly, lbl, th.Hi)
	}
	c.Fill(0, m.Y+m.H, c.W, 1, th.Faint)

	// info strip
	y0 := m.Y + m.H + 10
	h := c.H - footH - y0 - 4
	pw := (c.W - 4*6) / 3
	f := u.fS
	lh := f.height + 1
	px := 6
	c.Panel(th, f, px, y0, pw, h, "POSITION")
	if st.Err != nil {
		c.Text(u.fM, px+10, y0+20, "DECAYED / NO SOLUTION", th.Alert)
	} else {
		row := func(i int, k, val string, col color.RGBA) {
			c.Text(f, px+10, y0+10+i*lh, k, th.Dim)
			c.TextRight(f, px+pw-10, y0+10+i*lh, val, col)
		}
		row(0, "LAT", fmtLat(st.Geo.Lat), th.FG)
		row(1, "LON", fmtLon(st.Geo.Lon), th.FG)
		row(2, "ALT", fmt.Sprintf("%.1f KM", st.Geo.Alt), th.FG)
		row(3, "VEL", fmt.Sprintf("%.2f KM/S", st.Speed), th.FG)
	}
	px += pw + 6
	c.Panel(th, f, px, y0, pw, h, "ORBIT")
	{
		row := func(i int, k, val string, col color.RGBA) {
			c.Text(f, px+10, y0+10+i*lh, k, th.Dim)
			c.TextRight(f, px+pw-10, y0+10+i*lh, val, col)
		}
		row(0, "REV", commas(v.TLE.orbitNumber(now)), th.FG)
		row(1, "PERIOD", fmt.Sprintf("%.1f MIN", v.TLE.PeriodMin()), th.FG)
		lamp, col := "SUNLIT", th.FG
		switch st.Shadow {
		case Umbra:
			lamp, col = "ECLIPSE", th.Warn
		case Penumbra:
			lamp, col = "PENUMBRA", th.Warn
		}
		row(2, "SUN", lamp, col)
		if t, entering, ok := u.shadowEvent(v, now); ok {
			k := "SHADOW IN"
			if !entering {
				k = "SUNRISE IN"
			}
			row(3, k, fmtDur(t.Sub(now)), th.Hi)
		} else {
			row(3, "SHADOW", "NONE · FULL SUN", th.Dim)
		}
	}
	px += pw + 6
	u.drawObserverPanel(c, px, y0, pw, h, now, st)
}

// cached next-shadow lookup (it walks the orbit; no need every frame)
var shadowCache struct {
	catnr   int
	at, t   time.Time
	entering, ok bool
}

func (u *UI) shadowEvent(v *Vehicle, now time.Time) (time.Time, bool, bool) {
	sc := &shadowCache
	if sc.catnr != v.TLE.Catnr || now.Before(sc.at) || (sc.ok && !now.Before(sc.t)) || now.Sub(sc.at) > 10*time.Minute {
		sc.catnr, sc.at = v.TLE.Catnr, now
		sc.t, sc.entering, sc.ok = NextShadowChange(v.TLE, now)
	}
	return sc.t, sc.entering, sc.ok
}

func (u *UI) siteGeo() *Geo {
	if s := u.site(); s != nil {
		g := s.Geo()
		return &g
	}
	return nil
}

func (u *UI) drawObserverPanel(c *Canvas, px, y0, pw, h int, now time.Time, st State) {
	th := u.th
	f := u.fS
	s := u.site()
	title := "OBSERVER"
	if s != nil {
		title = strings.ToUpper(strings.Split(s.Name, ",")[0])
	}
	c.Panel(th, f, px, y0, pw, h, f.Ellipsize(title, pw-30))
	if s == nil {
		c.Text(f, px+10, y0+12, "NO SITE SET", th.Warn)
		c.Text(f, px+10, y0+34, "SYS > SITE", th.Dim)
		return
	}
	loc := s.Location()
	if st.Err == nil && st.Look.El > 0 {
		u.glow(c, u.fM, px+10, y0+8, "IN VIEW", th.Hi)
		c.Text(f, px+10, y0+34, fmt.Sprintf("EL %4.1f°", st.Look.El), th.FG)
		c.Text(f, px+10, y0+54, fmt.Sprintf("AZ %5.1f° %s", st.Look.Az, compass(st.Look.Az)), th.FG)
		if p := u.currentPass(now); p != nil {
			c.TextRight(f, px+pw-10, y0+12, "LOS "+fmtDur(p.LOS.Sub(now))[3:], th.Dim)
			if p.Visible && now.After(p.VisStart.Add(-time.Second)) && now.Before(p.VisEnd.Add(time.Second)) {
				c.TextRight(f, px+pw-10, y0+34, "★ LOOK UP", th.Warn)
			}
		}
		c.Text(f, px+10, y0+74, fmt.Sprintf("RNG %.0f KM", st.Look.Range), th.Dim)
		return
	}
	p := u.nextPass(now, false)
	if p == nil {
		msg := "COMPUTING…"
		if !u.passBusy {
			msg = "NO PASS IN 5 DAYS"
		}
		c.Text(f, px+10, y0+12, msg, th.Dim)
		return
	}
	c.Text(f, px+10, y0+8, "NEXT PASS", th.Dim)
	lt := p.AOS.In(loc)
	c.TextRight(f, px+pw-10, y0+8, strings.ToUpper(lt.Format("Mon 15:04")), th.FG)
	u.glow(c, u.fL, px+10, y0+24, "T-"+fmtDur(p.AOS.Sub(now)), th.Hi)
	vis := fmt.Sprintf("MAX %.0f°", p.MaxEl)
	c.Text(f, px+10, y0+66, vis, th.FG)
	if p.Visible {
		c.TextRight(f, px+pw-10, y0+66, "★ VISIBLE", th.Warn)
	} else {
		c.TextRight(f, px+pw-10, y0+66, "NOT VISIBLE", th.Faint)
	}
}

func (u *UI) currentPass(now time.Time) *Pass {
	for i := range u.passes {
		p := &u.passes[i]
		if !now.Before(p.AOS) && now.Before(p.LOS) {
			return p
		}
	}
	return nil
}

func (u *UI) nextPass(now time.Time, visible bool) *Pass {
	for i := range u.passes {
		p := &u.passes[i]
		if p.AOS.After(now) && (!visible || p.Visible) {
			return p
		}
	}
	return nil
}

func ll(t []TrackPt) [][2]float64 {
	out := make([][2]float64, len(t))
	for i, p := range t {
		out[i] = [2]float64{p.Lat, p.Lon}
	}
	return out
}

func fmtLat(l float64) string {
	h := "N"
	if l < 0 {
		h, l = "S", -l
	}
	return fmt.Sprintf("%.2f°%s", l, h)
}

func fmtLon(l float64) string {
	h := "E"
	if l < 0 {
		h, l = "W", -l
	}
	return fmt.Sprintf("%.2f°%s", l, h)
}

func commas(n int) string {
	s := itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}

// ---------- MASTER ----------

func (u *UI) drawMaster(c *Canvas, now time.Time) {
	th := u.th
	v := u.cur()
	st := v.TLE.StateAt(now, u.siteGeo())
	// globe
	gx, gy, gr := 166, headH+(c.H-headH-footH)/2+4, 150
	if u.follow && st.Err == nil {
		re := temeToECEF(st.R, now).Unit()
		hn := temeToECEF(st.R.Cross(st.V), now).Unit()
		d := re.Add(hn.Scale(0.6)).Unit()
		// quantise so the cached globe isn't redrawn for every sub-pixel move
		u.viewLat = math.Round(clamp(math.Asin(d[2])/deg, -70, 70)*2) / 2
		u.viewLon = math.Round(math.Atan2(d[1], d[0])/deg*2) / 2
	}
	g := newGlobe(gx, gy, gr, u.viewLat, u.viewLon)
	sun := subSolar(now)
	key := fmt.Sprintf("%d|%.1f|%.1f|%.1f|%.1f", u.themeIdx, g.Lat, g.Lon, sun.Lat, sun.Lon)
	if key != u.globeKey || u.globeImg == nil {
		u.globeImg = renderGlobe(th, g, sun)
		u.globeKey = key
	}
	k := 1.0
	if u.exaggerate {
		k = 4
	}
	toView := func(r Vec3, t time.Time) Vec3 { // TEME km -> ECEF Earth radii (altitude scaled)
		e := temeToECEF(r, t)
		n := e.Norm() / reWGS84
		return e.Unit().Scale(1 + (n-1)*k)
	}
	// orbit ring: one period of inertial positions, frozen at "now" Earth orientation
	per := v.TLE.PeriodMin()
	var ring []Vec3
	for i := 0; i <= 120; i++ {
		t := now.Add(time.Duration((float64(i)/120 - 0.5) * per * float64(time.Minute)))
		r, _, err := v.TLE.At(t)
		if err != nil {
			continue
		}
		ring = append(ring, toView(r, now))
	}
	// back half of the ring first (behind the globe)
	for i := 1; i < len(ring); i++ {
		x0, y0, f0 := g.Project(ring[i-1])
		x1, y1, f1 := g.Project(ring[i])
		if !f0 || !f1 {
			c.Line(int(x0), int(y0), int(x1), int(y1), th.Faint, 2)
		}
	}
	c.Blit(u.globeImg, gx-gr, gy-gr)
	for i := 1; i < len(ring); i++ {
		x0, y0, f0 := g.Project(ring[i-1])
		x1, y1, f1 := g.Project(ring[i])
		if f0 && f1 {
			col := th.Track
			if i < len(ring)/2 {
				col = th.Past
			}
			c.Line(int(x0), int(y0), int(x1), int(y1), col, 0)
		}
	}
	if s := u.site(); s != nil {
		x, y, front := g.Project(unitLL(s.Lat, s.Lon))
		if front {
			c.siteMark(int(x), int(y), th.Site)
		}
	}
	if st.Err == nil {
		p := toView(st.R, now)
		x, y, front := g.Project(p)
		sx, sy, sfront := g.Project(unitLL(st.Geo.Lat, st.Geo.Lon))
		if front {
			if sfront {
				c.Line(int(sx), int(sy), int(x), int(y), th.Dim, 2)
			}
			col := th.Hi
			if u.frame/5%2 == 1 {
				col = th.FG
			}
			c.crosshair(int(x), int(y), col, th.BG, false)
		}
	}
	mode := "FOLLOW"
	if !u.follow {
		mode = "FREE"
	}
	c.Text(u.fS, 8, headH+6, fmt.Sprintf("ITRF · %s", mode), th.Dim)
	alt := "ALT ×1 TRUE"
	if u.exaggerate {
		alt = "ALT ×4"
	}
	c.Text(u.fS, 8, c.H-footH-u.fS.height-4, alt, th.Dim)

	// right column
	x0, w := 338, c.W-338-6
	f := u.fS
	lh := f.height
	y := headH + 12
	row2 := func(yy int, k1, v1, k2, v2 string) {
		half := w / 2
		c.Text(f, x0+8, yy, k1, th.Dim)
		c.TextRight(f, x0+half-6, yy, v1, th.FG)
		if k2 != "" {
			c.Text(f, x0+half+6, yy, k2, th.Dim)
			c.TextRight(f, x0+w-8, yy, v2, th.FG)
		}
	}
	// state vector
	h := 3*lh + 12
	c.Panel(th, f, x0, y, w, h, "STATE · TEME KM, KM/S")
	if st.Err == nil {
		for i, n := range []string{"X", "Y", "Z"} {
			row2(y+8+i*lh, n, fmt.Sprintf("%.1f", st.R[i]), "V"+n, fmt.Sprintf("%.3f", st.V[i]))
		}
	}
	y += h + 12
	h = 2*lh + 12
	c.Panel(th, f, x0, y, w, h, "GEODETIC")
	if st.Err == nil {
		row2(y+8, "LAT", fmtLat(st.Geo.Lat), "ALT", fmt.Sprintf("%.1f", st.Geo.Alt))
		row2(y+8+lh, "LON", fmtLon(st.Geo.Lon), "BETA", fmt.Sprintf("%+.1f°", st.Beta))
	}
	y += h + 12
	h = 4*lh + 12
	age := now.Sub(v.TLE.Epoch)
	c.Panel(th, f, x0, y, w, h, "ELEMENTS · "+v.TLE.Intl)
	{
		apo, peri := v.TLE.ApsidesKm()
		ageCol := th.FG
		if age > 3*24*time.Hour || age < -24*time.Hour {
			ageCol = th.Warn
		}
		c.Text(f, x0+8, y+8, "EPOCH", th.Dim)
		c.TextRight(f, x0+w-8, y+8, v.TLE.Epoch.Format("02 Jan 15:04Z")+" ("+fmtAge(age)+")", ageCol)
		row2(y+8+lh, "INC", fmt.Sprintf("%.2f°", v.TLE.Incl), "ECC", fmt.Sprintf("%.5f", v.TLE.Ecc))
		row2(y+8+2*lh, "RAAN", fmt.Sprintf("%.1f°", v.TLE.RAAN), "PER", fmt.Sprintf("%.1fm", v.TLE.PeriodMin()))
		row2(y+8+3*lh, "APO", fmt.Sprintf("%.0f", apo), "PERI", fmt.Sprintf("%.0f", peri))
	}
	y += h + 12
	h = 3*lh + 12
	c.Panel(th, f, x0, y, w, h, "EVENTS")
	{
		loc := time.UTC
		if s := u.site(); s != nil {
			loc = s.Location()
		}
		ev := func(i int, k string, t time.Time, ok bool) {
			c.Text(f, x0+8, y+8+i*lh, k, th.Dim)
			if !ok {
				c.TextRight(f, x0+w-8, y+8+i*lh, "—", th.Faint)
				return
			}
			c.TextRight(f, x0+w-8, y+8+i*lh, strings.ToUpper(t.In(loc).Format("Mon 15:04"))+"  T-"+fmtDur(t.Sub(now)), th.FG)
		}
		t, entering, ok := u.shadowEvent(v, now)
		if entering || !ok {
			ev(0, "SHADOW", t, ok)
		} else {
			ev(0, "SUNRISE", t, ok)
		}
		var aos, vis time.Time
		if p := u.nextPass(now, false); p != nil {
			aos = p.AOS
		}
		if p := u.nextPass(now, true); p != nil {
			vis = p.AOS
		}
		ev(1, "AOS", aos, !aos.IsZero())
		ev(2, "VISIBLE", vis, !vis.IsZero())
	}
	y += h + 10
	// annunciators
	lamps := []struct {
		n  string
		on bool
		c  color.RGBA
	}{
		{"ECLIPSE", st.Shadow == Umbra, th.Warn},
		{"IN VIEW", st.Err == nil && st.Look.El > 0, th.Hi},
		{"WARP", !u.isLive(), th.Warn},
		{"STALE", age > 3*24*time.Hour, th.Alert},
	}
	lw := (w - 3*4) / 4
	for i, l := range lamps {
		lx := x0 + i*(lw+4)
		if l.on {
			c.Fill(lx, y, lw, 22, l.c)
			c.TextCenter(u.fS, lx+lw/2, y+1, l.n, th.BG)
		} else {
			c.Border(lx, y, lw, 22, 1, th.Faint)
			c.TextCenter(u.fS, lx+lw/2, y+1, l.n, th.Faint)
		}
	}
}

func fmtAge(d time.Duration) string {
	switch {
	case d < 0:
		return "FUTURE"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%.0fh", d.Hours())
	}
	return fmt.Sprintf("%.0fd", d.Hours()/24)
}

// ---------- PASS ----------

func (u *UI) drawPass(c *Canvas, now time.Time) {
	th := u.th
	f := u.fS
	s := u.site()
	if s == nil {
		c.TextCenter(u.fM, c.W/2, c.H/2-20, "SET AN OBSERVER SITE ON THE SYS PAGE", th.Warn)
		return
	}
	loc := s.Location()
	list := u.listPasses(now)
	if u.passSel >= len(list) {
		u.passSel = max(0, len(list)-1)
	}
	lx, lw := 6, 330
	y := headH + 8
	c.Text(f, lx+4, y, strings.ToUpper(s.Name), th.FG)
	tag := "ALL PASSES"
	if u.visOnly {
		tag = "★ VISIBLE ONLY"
	}
	c.TextRight(f, lx+lw-4, y, tag, th.Warn)
	y += f.height + 4
	c.Text(f, lx+4, y, "DAY      AOS   DUR  MAX  PATH", th.Dim)
	tz := now.In(loc).Format("MST")
	c.TextRight(f, lx+lw-4, y, tz, th.Faint)
	y += f.height + 2
	c.Fill(lx, y, lw, 1, th.Faint)
	y += 4
	rowH := f.height + 3
	rows := (c.H - footH - y - 6) / rowH
	if u.passSel < u.passScroll {
		u.passScroll = u.passSel
	}
	if u.passSel >= u.passScroll+rows {
		u.passScroll = u.passSel - rows + 1
	}
	if len(list) == 0 {
		msg := "COMPUTING PASSES…"
		if !u.passBusy {
			msg = "NONE IN THE NEXT 5 DAYS"
			if u.visOnly {
				msg = "NO VISIBLE PASSES IN 5 DAYS"
			}
		}
		c.Text(u.fM, lx+8, y+10, msg, th.Dim)
	}
	for i := u.passScroll; i < len(list) && i < u.passScroll+rows; i++ {
		p := list[i]
		ry := y + (i-u.passScroll)*rowH
		fg := th.FG
		if p.MaxEl < 15 {
			fg = th.Dim
		}
		if i == u.passSel {
			c.Fill(lx, ry-1, lw, rowH, th.SelBG)
			fg = th.SelFG
		}
		lt := p.AOS.In(loc)
		day := strings.ToUpper(lt.Format("Mon 02"))
		dur := p.Duration()
		line := fmt.Sprintf("%s  %s %2d:%02d %3.0f°  %s›%s", day, lt.Format("15:04"), int(dur.Minutes()), int(dur.Seconds())%60, p.MaxEl, compass(p.AOSAz), compass(p.LOSAz))
		if !now.Before(p.AOS) {
			line = fmt.Sprintf("NOW      %s %2d:%02d %3.0f°  %s›%s", lt.Format("15:04"), int(dur.Minutes()), int(dur.Seconds())%60, p.MaxEl, compass(p.AOSAz), compass(p.LOSAz))
		}
		c.Text(f, lx+4, ry, line, fg)
		if p.Visible {
			col := th.Warn
			if i == u.passSel {
				col = th.SelFG
			}
			c.TextRight(f, lx+lw-4, ry, "★", col)
		}
	}
	c.Fill(lx+lw+6, headH+6, 1, c.H-headH-footH-12, th.Faint)

	// sky plot
	if len(list) == 0 {
		return
	}
	p := list[u.passSel]
	sp := SkyPlot{CX: 492, CY: headH + 26 + 104, R: 104}
	c.skyGrid(th, f, sp)
	for i := 1; i < len(p.Track); i++ {
		a, b := p.Track[i-1], p.Track[i]
		x0, y0 := sp.XY(a.Az, a.El)
		x1, y1 := sp.XY(b.Az, b.El)
		if b.Lit {
			c.ThickLine(x0, y0, x1, y1, th.Track)
		} else {
			c.Line(x0, y0, x1, y1, th.Past, 2)
		}
	}
	ax, ay := sp.XY(p.AOSAz, 0)
	c.Disc(ax, ay, 3, th.FG)
	bx, by := sp.XY(p.LOSAz, 0)
	c.Border(bx-3, by-3, 7, 7, 1, th.FG)
	mx, my := sp.XY(p.MaxAz, p.MaxEl)
	c.Disc(mx, my, 2, th.Hi)
	if !now.Before(p.AOS) && now.Before(p.LOS) {
		st := u.cur().TLE.StateAt(now, u.siteGeo())
		if st.Look.El > 0 {
			x, y := sp.XY(st.Look.Az, st.Look.El)
			col := th.Hi
			if u.frame/5%2 == 1 {
				col = th.Warn
			}
			c.crosshair(x, y, col, th.BG, false)
		}
	}
	// details
	dx, dw := lx+lw+16, c.W-(lx+lw+16)-8
	dy := sp.CY + sp.R + 22
	line := func(i int, k string, t time.Time, extra string) {
		c.Text(f, dx, dy+i*(f.height), k, th.Dim)
		c.Text(f, dx+44, dy+i*(f.height), t.In(loc).Format("15:04:05"), th.FG)
		c.TextRight(f, dx+dw, dy+i*(f.height), extra, th.FG)
	}
	line(0, "AOS", p.AOS, fmt.Sprintf("%3.0f° %s", p.AOSAz, compass(p.AOSAz)))
	line(1, "MAX", p.TCA, fmt.Sprintf("EL %.0f° %s", p.MaxEl, compass(p.MaxAz)))
	line(2, "LOS", p.LOS, fmt.Sprintf("%3.0f° %s", p.LOSAz, compass(p.LOSAz)))
	yy := dy + 3*f.height + 2
	if p.Visible {
		c.Text(f, dx, yy, fmt.Sprintf("★ SEE IT %s–%s", p.VisStart.In(loc).Format("15:04"), p.VisEnd.In(loc).Format("15:04")), th.Warn)
	} else {
		c.Text(f, dx, yy, "NOT VISIBLE: "+u.whyNotVisible(p), th.Faint)
	}
	// countdown
	var cd string
	switch {
	case now.Before(p.AOS):
		cd = "AOS T-" + fmtDur(p.AOS.Sub(now))
	default:
		cd = "LOS T-" + fmtDur(p.LOS.Sub(now))
	}
	u.glow(c, u.fL, dx+(dw-u.fL.Width(cd))/2, yy+f.height+6, cd, th.Hi)
}

func (u *UI) whyNotVisible(p Pass) string {
	s := u.site()
	if s == nil {
		return ""
	}
	if p.MaxEl < 10 {
		return "TOO LOW"
	}
	if sunElevation(s.Geo(), p.TCA) > -6 {
		return "DAYLIGHT"
	}
	return "IN EARTH SHADOW"
}

// ---------- SYS ----------

func (u *UI) drawSys(c *Canvas, now time.Time) {
	th := u.th
	f := u.fS
	x, w := 8, c.W-16
	y := headH + 10
	cfg := u.env.Config
	site := "NOT SET"
	if s := cfg.Site; s != nil {
		site = fmt.Sprintf("%s  %s %s", strings.ToUpper(s.Name), fmtLat(s.Lat), fmtLon(s.Lon))
	}
	onoff := func(b bool) string {
		if b {
			return "ON"
		}
		return "OFF"
	}
	fetched := "NEVER"
	if !u.meta.Fetched.IsZero() {
		fetched = fmtAge(u.env.RealNow().Sub(u.meta.Fetched)) + " AGO"
	}
	if u.fetching {
		fetched = "DOWNLOADING…"
	} else if u.netErr != "" {
		fetched += " · " + u.netErr
	}
	rows := []struct{ k, v string }{
		{"OBSERVER SITE", site},
		{"THEME", strings.ToUpper(u.th.Name)},
		{"SCANLINES", onoff(cfg.Scanlines)},
		{"KIOSK PAGE TIME", fmt.Sprintf("%dS  (START TOGGLES KIOSK)", cfg.Kiosk)},
		{"UPDATE ELEMENTS", fetched},
		{"HELP", "BUTTONS AND SYMBOLS"},
	}
	h := len(rows)*(f.height+4) + 18
	c.Panel(th, f, x, y, w, h, "SETTINGS")
	for i, r := range rows {
		ry := y + 12 + i*(f.height+4)
		fg, kc := th.FG, th.Dim
		if i == u.sysSel {
			c.Fill(x+4, ry-1, w-8, f.height+3, th.SelBG)
			fg, kc = th.SelFG, th.SelFG
		}
		c.Text(f, x+10, ry, r.k, kc)
		c.TextRight(f, x+w-10, ry, f.Ellipsize(r.v, w-200), fg)
	}
	y += h + 14

	// elements
	n := min(len(u.fleet), 6)
	h = (n+1)*f.height + 14
	c.Panel(th, f, x, y, w, h, "ELEMENT SETS · CELESTRAK")
	c.Text(f, x+10, y+8, "VEHICLE", th.Dim)
	c.Text(f, x+300, y+8, "NORAD", th.Dim)
	c.TextRight(f, x+w-10, y+8, "EPOCH AGE", th.Dim)
	for i := 0; i < n; i++ {
		vv := u.fleet[i]
		ry := y + 8 + (i+1)*f.height
		name := vv.TLE.Name
		if vv.Host != "" {
			name += " · DOCKED " + vv.Host
		}
		col := th.FG
		if i == u.sel {
			col = th.Hi
		}
		c.Text(f, x+10, ry, f.Ellipsize(name, 280), col)
		c.Text(f, x+300, ry, itoa(vv.TLE.Catnr), th.Dim)
		age := now.Sub(vv.TLE.Epoch)
		ac := th.FG
		if age > 3*24*time.Hour {
			ac = th.Warn
		}
		c.TextRight(f, x+w-10, ry, fmtAge(age), ac)
	}
	if len(u.fleet) > n {
		c.TextRight(f, x+w-10, y+h-f.height-2, fmt.Sprintf("+%d MORE (X CYCLES)", len(u.fleet)-n), th.Faint)
	}
	y += h + 14

	// clock + about
	h = c.H - footH - y - 8
	c.Panel(th, f, x, y, w, h, "CLOCK · ABOUT")
	clk := "DEVICE CLOCK"
	if off, ok := u.env.Store.ClockOffset(); ok && off != 0 {
		clk = "CORRECTED FROM CELESTRAK " + fmtDur(off)
	} else if ok {
		clk = "DEVICE CLOCK · CHECKED OK"
	}
	loc := time.UTC
	if s := cfg.Site; s != nil {
		loc = s.Location()
	}
	c.Text(f, x+10, y+8, clk, th.FG)
	c.TextRight(f, x+w-10, y+8, now.In(loc).Format("Mon 02 Jan 15:04 MST"), th.FG)
	c.Text(f, x+10, y+8+f.height, "MIYOO MCTL "+version+" · AFTER MCTL BY LIAM DROOG", th.Dim)
	c.Text(f, x+10, y+8+2*f.height, "ELEMENTS CELESTRAK · MAP NATURAL EARTH · SGP4 VALLADO", th.Faint)
}

// ---------- modals ----------

func (u *UI) drawSites(c *Canvas) {
	th := u.th
	f := u.fM
	c.Fill(0, 0, c.W, c.H, th.BG)
	u.glow(c, u.fM, 16, 10, "SELECT OBSERVER SITE", th.Hi)
	c.TextRight(u.fS, c.W-16, 14, "PASSES ARE PREDICTED FOR THIS PLACE", th.Dim)
	c.Fill(16, 40, c.W-32, 1, th.Faint)
	rowH := f.height + 2
	top := 48
	rows := (c.H - top - 34) / rowH
	scroll := max(0, u.siteSel-rows/2)
	n := len(presetSites) + 1
	scroll = min(scroll, max(0, n-rows))
	for i := scroll; i < n && i < scroll+rows; i++ {
		y := top + (i-scroll)*rowH
		name, extra := "", ""
		if i == 0 {
			name, extra = "◆ CUSTOM LAT / LON…", ""
		} else {
			s := presetSites[i-1]
			name = s.Name
			extra = fmtLat(s.Lat) + " " + fmtLon(s.Lon)
		}
		fg, dc := th.FG, th.Dim
		if i == u.siteSel {
			c.Fill(12, y-1, c.W-24, rowH, th.SelBG)
			fg, dc = th.SelFG, th.SelFG
		}
		c.Text(f, 24, y, name, fg)
		c.TextRight(u.fS, c.W-24, y+3, extra, dc)
	}
	c.Fill(0, c.H-28, c.W, 28, th.Panel)
	hint := "↑↓ MOVE  ←→ JUMP 10  A SELECT"
	if u.site() != nil {
		hint += "  B CANCEL"
	} else {
		hint += "  MENU QUIT"
	}
	c.Text(u.fS, 12, c.H-24, hint, th.Dim)
	if u.env.Config.Scanlines && th.Scan > 0 {
		// Draw adds scanlines after us
	}
}

func (u *UI) drawEdit(c *Canvas) {
	th := u.th
	c.Fill(0, 0, c.W, c.H, th.BG)
	u.glow(c, u.fM, 16, 10, "CUSTOM SITE", th.Hi)
	c.Fill(16, 40, c.W-32, 1, th.Faint)
	fields := []struct {
		k string
		v string
	}{{"LATITUDE", fmtLat(u.editSite.Lat)}, {"LONGITUDE", fmtLon(u.editSite.Lon)}}
	for i, fl := range fields {
		y := 80 + i*90
		col := th.Dim
		if i == u.editField {
			c.Border(40, y-8, 280, 74, 2, th.FG)
			col = th.FG
		}
		c.Text(u.fM, 60, y, fl.k, col)
		u.glow(c, u.fL, 60, y+22, fl.v, th.Hi)
	}
	// little map to sanity-check the spot
	m := MapView{c.W - 290, 100, 250, 125}
	small := baseMap(th, m.W, m.H)
	c.Blit(small, m.X, m.Y)
	c.Border(m.X-1, m.Y-1, m.W+2, m.H+2, 1, th.Faint)
	x, y := m.XY(u.editSite.Lat, u.editSite.Lon)
	c.siteMark(int(x), int(y), th.Site)
	c.Text(u.fS, 40, 280, "TIME ZONE STAYS "+u.editSite.TZ+" · CHANGE IT IN data/config.json", th.Faint)
	c.Fill(0, c.H-28, c.W, 28, th.Panel)
	c.Text(u.fS, 12, c.H-24, "X FIELD  ↑↓ 0.1°  ←→ 1°  L1/R1 10°  A SAVE  B BACK", th.Dim)
}

func (u *UI) drawHelp(c *Canvas) {
	th := u.th
	x, y, w, h := 40, 44, c.W-80, c.H-44-footH-14
	c.Fill(x, y, w, h, th.Panel)
	c.Border(x, y, w, h, 2, th.FG)
	f := u.fS
	lines := [][2]string{
		{"L1 / R1", "page: TRACK · MASTER · PASS · SYS"},
		{"X", "next vehicle"},
		{"L2 / R2", "time warp back / forward (×10 … ×3600)"},
		{"Y", "back to live time"},
		{"SELECT", "theme: phosphor · amber · retrowave · modern"},
		{"START", "kiosk: cycle pages, keep screen on"},
		{"MENU", "quit"},
		{"", ""},
		{"[+] / ◆", "vehicle / your observer site"},
		{"solid / dashed", "track in sunlight / in Earth's shadow"},
		{"★", "visible pass: lit vehicle, dark sky, >10°"},
		{"dotted ring", "footprint — where it's above the horizon"},
	}
	u.glow(c, u.fM, x+16, y+10, "HELP", th.Hi)
	for i, l := range lines {
		yy := y + 44 + i*(f.height+1)
		c.Text(f, x+16, yy, l[0], th.FG)
		c.Text(f, x+150, yy, l[1], th.Dim)
	}
	c.TextRight(f, x+w-12, y+h-f.height-6, "ANY BUTTON CLOSES", th.Faint)
}

// unused guard so os stays imported when building on non-device targets
var _ = os.Getenv
