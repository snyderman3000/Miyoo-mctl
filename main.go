// Miyoo mctl: a mission-control spacecraft tracker for OnionOS on the Miyoo Mini Plus.
// An unofficial handheld take on mctl by Liam Droog (github.com/LiamDroog/mctl);
// written from scratch in Go, sharing no code with the original.
package main

import (
	"flag"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const version = "0.1.0"
const userAgent = "Miyoo-mctl/" + version + " (OnionOS spacecraft tracker; +https://github.com/snyderman3000/Miyoo-mctl)"

type Env struct {
	AppDir, DataDir string
	Config          Config
	Store           *Store
	ClockOffset     time.Duration
	Demo            bool      // screenshots: fixed clock, no network
	Sync            bool      // compute passes inline (screenshots, tests)
	DemoNow         time.Time
	Device          bool
}

func (e *Env) RealNow() time.Time {
	if e.Demo {
		return e.DemoNow
	}
	return time.Now().Add(e.ClockOffset)
}

func (e *Env) configPath() string { return filepath.Join(e.DataDir, "config.json") }

func (e *Env) SaveConfig() {
	if e.DataDir != "" && !e.Demo {
		saveConfig(e.configPath(), e.Config)
	}
}

// StayAwake asks Onion not to auto-sleep (kiosk mode).
func (e *Env) StayAwake(on bool) {
	if !e.Device {
		return
	}
	if on {
		os.WriteFile("/tmp/stay_awake", nil, 0o644)
	} else {
		os.Remove("/tmp/stay_awake")
	}
}

func newEnv(dataDir string) *Env {
	os.MkdirAll(dataDir, 0o755)
	env := &Env{DataDir: dataDir}
	env.Config = loadConfig(env.configPath())
	env.Store = &Store{Dir: dataDir, UA: userAgent, HTTP: &http.Client{Transport: newTransport(), Timeout: 40 * time.Second}}
	tlsClockOffset = env.Store.offset
	return env
}

func main() {
	shot := flag.String("shot", "", "render a demo scene (track, master, pass, sys, sites, edit, help, nodata) to PNG")
	out := flag.String("out", "shot.png", "PNG path for -shot")
	theme := flag.String("theme", "", "theme for -shot")
	w := flag.Int("w", 640, "screen width for -shot")
	h := flag.Int("h", 480, "screen height for -shot")
	icon := flag.String("icon", "", "write the OnionOS app icon to this PNG path and exit")
	tles := flag.String("tle", "", "with -shot: use this element file instead of the demo set")
	at := flag.String("at", "", "with -shot: RFC3339 time to render")
	flag.Parse()
	if *icon != "" {
		f, err := os.Create(*icon)
		if err != nil {
			log.Fatal(err)
		}
		png.Encode(f, appIcon(74))
		f.Close()
		return
	}
	if *shot != "" {
		tmp, _ := os.MkdirTemp("", "miyoo-mctl-shot")
		defer os.RemoveAll(tmp)
		env := newEnv(tmp)
		env.Demo, env.Sync = true, true
		env.DemoNow = demoNow
		if *at != "" {
			t, err := time.Parse(time.RFC3339, *at)
			if err != nil {
				log.Fatal(err)
			}
			env.DemoNow = t
		}
		if *theme != "" {
			env.Config.Theme = *theme
		}
		c := NewCanvas(*w, *h)
		ui := NewUI(env, c.W, c.H)
		text := demoTLEs()
		if *tles != "" {
			b, err := os.ReadFile(*tles)
			if err != nil {
				log.Fatal(err)
			}
			text = string(b)
		}
		if err := demoScene(ui, *shot, text); err != nil {
			log.Fatal(err)
		}
		ui.Draw(c)
		f, err := os.Create(*out)
		if err != nil {
			log.Fatal(err)
		}
		png.Encode(f, c)
		f.Close()
		return
	}

	exe, _ := os.Executable()
	appDir := filepath.Dir(exe)
	dataDir := filepath.Join(appDir, "data")
	if d := os.Getenv("MCTL_DATA"); d != "" {
		dataDir = d
	}
	os.MkdirAll(dataDir, 0o755)
	logPath := filepath.Join(dataDir, "mctl.log")
	if st, err := os.Stat(logPath); err == nil && st.Size() > 512<<10 {
		os.Rename(logPath, logPath+".1")
	}
	if logf, _ := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); logf != nil {
		log.SetOutput(logf)
	}
	env := newEnv(dataDir)
	env.AppDir = appDir
	env.Device = true
	log.Printf("miyoo-mctl %s starting", version)

	fb, err := OpenFramebuffer()
	if err != nil {
		log.Fatalf("framebuffer: %v", err)
	}
	defer fb.Close()
	c := NewCanvas(fb.W, fb.H)
	keys := make(chan int, 32)
	if err := ReadInput("/dev/input/event0", keys); err != nil {
		log.Fatalf("input: %v", err)
	}
	ui := NewUI(env, c.W, c.H)
	ui.Draw(c) // first frame before any slow work
	fb.Present(c)
	ui.Start()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	lastDraw := time.Time{}
	for !ui.quit {
		dirty := false
		select {
		case k := <-keys:
			if k == BtnY && ui.cur() == nil {
				ui.refresh(false)
			}
			ui.Key(k)
			dirty = true
		case f := <-ui.post:
			f()
			dirty = true
		case <-tick.C:
			ui.frame++
			ui.Tick()
			// live: twice a second (blinking marker, clock); warping: as fast as we can sensibly go
			every := 500 * time.Millisecond
			if !ui.isLive() {
				every = 200 * time.Millisecond
			}
			if time.Since(lastDraw) >= every {
				dirty = true
			}
		}
		if dirty {
			ui.Draw(c)
			fb.Present(c)
			lastDraw = time.Now()
		}
	}
	env.StayAwake(false)
	fb.Clear()
	fmt.Fprintln(os.Stderr, "bye")
}

// ---------- demo data for screenshots ----------

var demoNow = time.Date(2019, 6, 5, 20, 9, 40, 0, time.UTC)

func fixSum(l string) string {
	sum := 0
	for _, c := range l[:68] {
		if c >= '0' && c <= '9' {
			sum += int(c - '0')
		} else if c == '-' {
			sum++
		}
	}
	return l[:68] + itoa(sum%10)
}

func demoTLEs() string {
	sets := [][3]string{
		{"ISS (ZARYA)", "1 25544U 98067A   19156.50900463  .00003075  00000-0  59442-4 0  9992",
			"2 25544  51.6433  59.2583 0008217  16.4489 347.6017 15.51174618173442"},
		{"CSS (TIANHE)", "1 48274U 21035A   19156.50000000  .00020000  00000-0  22000-3 0  9990",
			"2 48274  41.4700 210.0000 0005000 300.0000  60.0000 15.60000000 10000"},
		{"HST", "1 20580U 90037B   19156.50000000  .00000800  00000-0  35000-4 0  9990",
			"2 20580  28.4700 120.0000 0002500 100.0000 260.0000 15.09000000 10000"},
		{"CREW DRAGON 1", "1 44063U 19011A   19156.50900463  .00003075  00000-0  59442-4 0  9992",
			"2 44063  51.6433  59.2583 0008217  16.4489 347.6017 15.51174618173442"},
		{"PROGRESS-MS 12", "1 44455U 19044A   19156.50900463  .00003075  00000-0  59442-4 0  9992",
			"2 44455  51.6433  59.2583 0008217  16.4489 347.6017 15.51174618173442"},
	}
	var b strings.Builder
	for _, s := range sets {
		b.WriteString(s[0] + "\n" + fixSum(s[1]) + "\n" + fixSum(s[2]) + "\n")
	}
	return b.String()
}

func demoScene(u *UI, scene, text string) error {
	u.setFleet(ParseTLEText(text))
	s := presetSites[0]
	u.env.Config.Site = &s
	u.meta.Fetched = u.env.DemoNow.Add(-90 * time.Minute)
	u.frame = 0
	u.ensurePasses(u.now())
	switch scene {
	case "track":
		u.page = pgTrack
	case "master":
		u.page = pgMaster
	case "pass":
		u.page = pgPass
		u.passSel = 1
	case "passvis":
		u.page = pgPass
		u.visOnly = true
	case "sys":
		u.page = pgSys
	case "sites":
		u.modal = modalSites
		u.siteSel = 0
	case "edit":
		u.modal = modalEdit
		u.editSite = Site{Name: "Custom", Lat: 35.0, Lon: -90.0, TZ: "America/Chicago"}
	case "help":
		u.modal = modalHelp
	case "nodata":
		u.fleet = nil
		u.netErr = "NO NETWORK"
	case "warp":
		u.page = pgTrack
		u.setWarp(7)
	default:
		return fmt.Errorf("unknown scene %q", scene)
	}
	return nil
}
