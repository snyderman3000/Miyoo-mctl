package main

// Config, observer sites, the vehicle fleet and the CelesTrak element download.

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // the device has no zoneinfo; bundle it
)

// Site is the observer.
type Site struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
	AltM float64 `json:"alt_m"`
	TZ   string  `json:"timezone"`
}

func (s Site) Geo() Geo { return Geo{s.Lat, s.Lon, s.AltM / 1000} }

func (s Site) Location() *time.Location {
	if s.TZ != "" {
		if l, err := time.LoadLocation(s.TZ); err == nil {
			return l
		}
	}
	return time.UTC
}

// Config lives in App/Miyoo-mctl/data/config.json.
type Config struct {
	Theme     string `json:"theme"`
	Site      *Site  `json:"site"`
	Scanlines bool   `json:"scanlines"`
	Kiosk     int    `json:"kiosk_seconds"` // seconds per page in kiosk mode
	Extra     []int  `json:"extra_catnrs"`  // more NORAD catalog numbers to track (low Earth orbit only)
	Vehicle   int    `json:"vehicle"`       // last selected catalog number
}

var presetSites = []Site{
	{"Memphis, TN", 35.149, -90.049, 85, "America/Chicago"},
	{"Atlanta, GA", 33.749, -84.388, 320, "America/New_York"},
	{"Boston, MA", 42.360, -71.059, 10, "America/New_York"},
	{"Cape Canaveral, FL", 28.392, -80.608, 3, "America/New_York"},
	{"Chicago, IL", 41.878, -87.630, 180, "America/Chicago"},
	{"Dallas, TX", 32.777, -96.797, 140, "America/Chicago"},
	{"Denver, CO", 39.739, -104.990, 1610, "America/Denver"},
	{"Houston, TX", 29.760, -95.370, 15, "America/Chicago"},
	{"Kansas City, MO", 39.100, -94.579, 270, "America/Chicago"},
	{"Las Vegas, NV", 36.170, -115.140, 610, "America/Los_Angeles"},
	{"Los Angeles, CA", 34.052, -118.244, 90, "America/Los_Angeles"},
	{"Miami, FL", 25.762, -80.192, 2, "America/New_York"},
	{"Minneapolis, MN", 44.978, -93.265, 260, "America/Chicago"},
	{"Nashville, TN", 36.163, -86.781, 170, "America/Chicago"},
	{"New Orleans, LA", 29.951, -90.072, 2, "America/Chicago"},
	{"New York, NY", 40.713, -74.006, 10, "America/New_York"},
	{"Phoenix, AZ", 33.448, -112.074, 330, "America/Phoenix"},
	{"Salt Lake City, UT", 40.761, -111.891, 1290, "America/Denver"},
	{"San Francisco, CA", 37.775, -122.419, 15, "America/Los_Angeles"},
	{"Seattle, WA", 47.606, -122.332, 50, "America/Los_Angeles"},
	{"St. Louis, MO", 38.627, -90.199, 140, "America/Chicago"},
	{"Washington, DC", 38.907, -77.037, 20, "America/New_York"},
	{"Anchorage, AK", 61.218, -149.900, 30, "America/Anchorage"},
	{"Honolulu, HI", 21.307, -157.858, 5, "Pacific/Honolulu"},
	{"Toronto, ON", 43.653, -79.383, 80, "America/Toronto"},
	{"Montreal, QC", 45.502, -73.567, 40, "America/Toronto"},
	{"Vancouver, BC", 49.283, -123.121, 20, "America/Vancouver"},
	{"Winnipeg, MB", 49.895, -97.138, 240, "America/Winnipeg"},
	{"Edmonton, AB", 53.546, -113.494, 650, "America/Edmonton"},
	{"Mexico City", 19.433, -99.133, 2240, "America/Mexico_City"},
	{"Bogota", 4.711, -74.072, 2640, "America/Bogota"},
	{"Lima", -12.046, -77.043, 150, "America/Lima"},
	{"Sao Paulo", -23.551, -46.633, 760, "America/Sao_Paulo"},
	{"Buenos Aires", -34.604, -58.382, 25, "America/Argentina/Buenos_Aires"},
	{"Santiago", -33.449, -70.669, 570, "America/Santiago"},
	{"London", 51.507, -0.128, 20, "Europe/London"},
	{"Dublin", 53.350, -6.260, 10, "Europe/Dublin"},
	{"Paris", 48.857, 2.352, 40, "Europe/Paris"},
	{"Amsterdam", 52.368, 4.904, 0, "Europe/Amsterdam"},
	{"Berlin", 52.520, 13.405, 35, "Europe/Berlin"},
	{"Madrid", 40.417, -3.704, 650, "Europe/Madrid"},
	{"Rome", 41.903, 12.496, 20, "Europe/Rome"},
	{"Stockholm", 59.329, 18.069, 20, "Europe/Stockholm"},
	{"Warsaw", 52.230, 21.012, 100, "Europe/Warsaw"},
	{"Athens", 37.984, 23.728, 70, "Europe/Athens"},
	{"Istanbul", 41.008, 28.978, 40, "Europe/Istanbul"},
	{"Moscow", 55.756, 37.617, 150, "Europe/Moscow"},
	{"Cairo", 30.044, 31.236, 25, "Africa/Cairo"},
	{"Lagos", 6.524, 3.379, 40, "Africa/Lagos"},
	{"Nairobi", -1.292, 36.822, 1700, "Africa/Nairobi"},
	{"Johannesburg", -26.204, 28.047, 1750, "Africa/Johannesburg"},
	{"Dubai", 25.205, 55.271, 10, "Asia/Dubai"},
	{"Mumbai", 19.076, 72.878, 10, "Asia/Kolkata"},
	{"Delhi", 28.614, 77.209, 215, "Asia/Kolkata"},
	{"Bangkok", 13.756, 100.502, 5, "Asia/Bangkok"},
	{"Singapore", 1.352, 103.820, 15, "Asia/Singapore"},
	{"Jakarta", -6.208, 106.846, 10, "Asia/Jakarta"},
	{"Manila", 14.600, 120.984, 10, "Asia/Manila"},
	{"Hong Kong", 22.320, 114.169, 30, "Asia/Hong_Kong"},
	{"Beijing", 39.904, 116.407, 45, "Asia/Shanghai"},
	{"Shanghai", 31.230, 121.474, 5, "Asia/Shanghai"},
	{"Seoul", 37.567, 126.978, 40, "Asia/Seoul"},
	{"Tokyo", 35.676, 139.650, 40, "Asia/Tokyo"},
	{"Perth", -31.950, 115.860, 30, "Australia/Perth"},
	{"Sydney", -33.869, 151.209, 30, "Australia/Sydney"},
	{"Melbourne", -37.814, 144.963, 30, "Australia/Melbourne"},
	{"Auckland", -36.849, 174.763, 30, "Pacific/Auckland"},
}

func loadConfig(path string) Config {
	cfg := Config{Theme: "phosphor", Scanlines: true, Kiosk: 20, Vehicle: 25544}
	b, err := os.ReadFile(path)
	if err == nil {
		if err := json.Unmarshal(b, &cfg); err != nil {
			log.Printf("config.json is not valid JSON (%v); using defaults", err)
		}
	}
	if cfg.Kiosk < 5 {
		cfg.Kiosk = 20
	}
	return cfg
}

func saveConfig(path string, cfg Config) {
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		log.Printf("config: %v", err)
	}
}

// ---------- fleet ----------

// Vehicle is one tracked object.
type Vehicle struct {
	TLE   *TLE
	Short string // header name
	Host  string // "ISS" when docked/berthed (CelesTrak gives it the station's elements)
}

var shortNames = map[int]string{25544: "ISS", 48274: "TIANGONG", 20580: "HUBBLE"}

// visiting-vehicle names worth listing from the stations group
var visitorWords = []string{"DRAGON", "SOYUZ", "PROGRESS", "CYGNUS", "HTV", "STARLINER", "TIANZHOU", "SHENZHOU", "DREAM CHASER"}

// buildFleet picks the vehicles worth showing out of the downloaded element sets.
func buildFleet(tles []*TLE, extra []int, now time.Time) []*Vehicle {
	keep := map[int]bool{}
	for _, c := range extra {
		keep[c] = true
	}
	var out []*Vehicle
	seen := map[int]bool{}
	for _, t := range tles {
		if seen[t.Catnr] {
			continue
		}
		name := strings.ToUpper(t.Name)
		ok := shortNames[t.Catnr] != "" || keep[t.Catnr]
		for _, w := range visitorWords {
			if strings.Contains(name, w) {
				ok = true
			}
		}
		if !ok {
			continue
		}
		seen[t.Catnr] = true
		v := &Vehicle{TLE: t, Short: shortNames[t.Catnr]}
		if v.Short == "" {
			v.Short = t.Name
		}
		out = append(out, v)
	}
	rank := func(v *Vehicle) int {
		switch v.TLE.Catnr {
		case 25544:
			return 0
		case 48274:
			return 1
		case 20580:
			return 2
		}
		return 3
	}
	sort.SliceStable(out, func(i, j int) bool {
		if rank(out[i]) != rank(out[j]) {
			return rank(out[i]) < rank(out[j])
		}
		return out[i].Short < out[j].Short
	})
	// docked visitors share their station's position
	for _, v := range out {
		if rank(v) < 3 {
			continue
		}
		for _, host := range out[:min(2, len(out))] {
			if rank(host) > 1 {
				continue
			}
			a, _, e1 := v.TLE.At(now)
			b, _, e2 := host.TLE.At(now)
			if e1 == nil && e2 == nil && a.Sub(b).Norm() < 5 {
				v.Host = host.Short
			}
		}
	}
	return out
}

// ---------- download ----------

const fetchEvery = 6 * time.Hour      // CelesTrak updates a few times a day
const fetchMinGap = 2 * time.Hour     // and asks clients not to hammer it

var celestrakURLs = []string{
	"https://celestrak.org/NORAD/elements/gp.php?GROUP=stations&FORMAT=tle",
	"https://celestrak.org/NORAD/elements/gp.php?CATNR=20580&FORMAT=tle",
}

type Meta struct {
	Fetched time.Time `json:"fetched"`
}

// Store keeps the element cache on the SD card and the clock correction.
type Store struct {
	Dir       string
	UA        string
	HTTP      *http.Client
	mu        sync.Mutex
	clockOff  time.Duration
	clockSync bool
}

func newTransport() *http.Transport {
	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSClientConfig:       &tls.Config{NextProtos: []string{"http/1.1"}, Time: tlsTime},
		TLSNextProto:          map[string]func(string, *tls.Conn) http.RoundTripper{},
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
}

// tlsTime lets certificate checks use the corrected clock when the device's is wrong.
var tlsClockOffset func() time.Duration

func tlsTime() time.Time {
	if tlsClockOffset != nil {
		return time.Now().Add(tlsClockOffset())
	}
	return time.Now()
}

// syncClockPlain reads the time from CelesTrak's plain-HTTP redirect, for when
// the device clock is so far off that TLS certificates look invalid.
func (s *Store) syncClockPlain() bool {
	c := &http.Client{Transport: newTransport(), Timeout: 15 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Head("http://celestrak.org/")
	if err != nil {
		return false
	}
	resp.Body.Close()
	d, err := http.ParseTime(resp.Header.Get("Date"))
	if err != nil {
		return false
	}
	s.mu.Lock()
	s.clockOff, s.clockSync = time.Until(d), true
	s.mu.Unlock()
	log.Printf("clock: device is off by %v; corrected from HTTP Date", -time.Until(d))
	return true
}

func (s *Store) tlePath() string  { return filepath.Join(s.Dir, "elements.txt") }
func (s *Store) metaPath() string { return filepath.Join(s.Dir, "elements.json") }

func (s *Store) Load() ([]*TLE, Meta) {
	var m Meta
	if b, err := os.ReadFile(s.metaPath()); err == nil {
		json.Unmarshal(b, &m)
	}
	b, err := os.ReadFile(s.tlePath())
	if err != nil {
		return nil, m
	}
	return ParseTLEText(string(b)), m
}

// ClockOffset is how far the device clock is behind the server's (only used when it's off by a minute or more).
func (s *Store) ClockOffset() (time.Duration, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.clockOff, s.clockSync
}

// Fetch downloads fresh elements and saves them; extra catalog numbers are fetched one by one.
func (s *Store) Fetch(extra []int) ([]*TLE, Meta, error) {
	urls := append([]string{}, celestrakURLs...)
	for _, c := range extra {
		if c != 20580 {
			urls = append(urls, "https://celestrak.org/NORAD/elements/gp.php?CATNR="+strconv.Itoa(c)+"&FORMAT=tle")
		}
	}
	var all strings.Builder
	got := 0
	var firstErr error
	for i, u := range urls {
		body, err := s.get(u)
		if err != nil && i == 0 && strings.Contains(err.Error(), "certificate") && s.syncClockPlain() {
			body, err = s.get(u) // device clock was wrong; try again with the corrected one
		}
		if err != nil {
			log.Printf("fetch %s: %v", u, err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if n := len(ParseTLEText(body)); n == 0 {
			log.Printf("fetch %s: no element sets in reply", u)
			continue
		}
		all.WriteString(strings.TrimSpace(body))
		all.WriteString("\n")
		got++
	}
	if got == 0 {
		if firstErr == nil {
			firstErr = errors.New("no element sets received")
		}
		return nil, Meta{}, firstErr
	}
	m := Meta{Fetched: time.Now().Add(s.offset())}
	os.WriteFile(s.tlePath()+".tmp", []byte(all.String()), 0o644)
	os.Rename(s.tlePath()+".tmp", s.tlePath())
	b, _ := json.Marshal(m)
	os.WriteFile(s.metaPath(), b, 0o644)
	return ParseTLEText(all.String()), m, nil
}

func (s *Store) offset() time.Duration {
	o, _ := s.ClockOffset()
	return o
}

func (s *Store) get(u string) (string, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", s.UA)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	// The Miyoo has no network time unless Onion's NTP option is on; trust the server's clock.
	if d, err := http.ParseTime(resp.Header.Get("Date")); err == nil {
		off := time.Until(d)
		s.mu.Lock()
		if off > time.Minute || off < -time.Minute {
			s.clockOff, s.clockSync = off, true
		} else {
			s.clockOff, s.clockSync = 0, true
		}
		s.mu.Unlock()
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b[:min(len(b), 120)])))
	}
	return string(b), nil
}
