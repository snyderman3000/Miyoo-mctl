package main

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

func TestSGP4AgainstReference(t *testing.T) {
	b, err := os.ReadFile("testdata/sgp4_ref.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref map[string]struct {
		L1, L2 string
		Pts    []struct {
			T    float64
			E    int
			R, V [3]float64
		}
	}
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatal(err)
	}
	for name, r := range ref {
		tle, err := ParseTLE(name, r.L1, r.L2)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, p := range r.Pts {
			pos, vel, err := tle.Propagate(p.T)
			if p.E != 0 {
				if err == nil {
					t.Errorf("%s t=%v: expected error %d", name, p.T, p.E)
				}
				continue
			}
			if err != nil {
				t.Errorf("%s t=%v: %v", name, p.T, err)
				continue
			}
			dr := Vec3(p.R).Sub(pos).Norm()
			dv := Vec3(p.V).Sub(vel).Norm()
			if dr > 1e-6 || dv > 1e-9 {
				t.Errorf("%s t=%v: dr=%.3g km dv=%.3g km/s", name, p.T, dr, dv)
			}
		}
	}
}

func TestPassesAgainstSkyfield(t *testing.T) {
	b, _ := os.ReadFile("testdata/sgp4_ref.json")
	var ref map[string]struct{ L1, L2 string }
	json.Unmarshal(b, &ref)
	tle, err := ParseTLE("ISS", ref["ISS"].L1, ref["ISS"].L2)
	if err != nil {
		t.Fatal(err)
	}
	var want []struct {
		AOS, TCA, LOS string
		MaxEl         float64
	}
	b, _ = os.ReadFile("testdata/passes_ref.json")
	json.Unmarshal(b, &want)
	site := Geo{35.05, -90.0, 0}
	from := time.Date(2019, 6, 5, 12, 0, 0, 0, time.UTC)
	got := PredictPasses(tle, site, from, 48*time.Hour, 50)
	if len(got) != len(want) {
		t.Fatalf("got %d passes, want %d", len(got), len(want))
	}
	for i, w := range want {
		pa := func(s string) time.Time { x, _ := time.Parse(time.RFC3339, s); return x }
		g := got[i]
		for _, c := range []struct {
			n    string
			a, b time.Time
		}{{"AOS", g.AOS, pa(w.AOS)}, {"TCA", g.TCA, pa(w.TCA)}, {"LOS", g.LOS, pa(w.LOS)}} {
			if d := c.a.Sub(c.b); d > 3*time.Second || d < -3*time.Second {
				t.Errorf("pass %d %s off by %v", i, c.n, d)
			}
		}
		if math.Abs(g.MaxEl-w.MaxEl) > 0.1 {
			t.Errorf("pass %d max el %.2f want %.2f", i, g.MaxEl, w.MaxEl)
		}
	}
}

func TestGeodeticRoundTrip(t *testing.T) {
	g := Geo{35.05, -90.0, 0.1}
	h := ecefToGeo(geoToECEF(g))
	if math.Abs(h.Lat-g.Lat) > 1e-9 || math.Abs(h.Lon-g.Lon) > 1e-9 || math.Abs(h.Alt-g.Alt) > 1e-6 {
		t.Fatalf("round trip %v -> %v", g, h)
	}
}

func TestSubSolarSolstice(t *testing.T) {
	// June solstice 2024-06-20 20:51 UTC: Sun overhead near 23.44N
	s := subSolar(time.Date(2024, 6, 20, 20, 51, 0, 0, time.UTC))
	if math.Abs(s.Lat-23.44) > 0.05 {
		t.Fatalf("subsolar lat %.3f", s.Lat)
	}
	// local noon at Greenwich around 12:00 UTC: lon near 0 (equation of time < 4.5 deg)
	s = subSolar(time.Date(2024, 6, 20, 12, 0, 0, 0, time.UTC))
	if math.Abs(s.Lon) > 5 {
		t.Fatalf("subsolar lon %.3f", s.Lon)
	}
}
