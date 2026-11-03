package main

import (
	"testing"
	"time"
)

func demoUI(t *testing.T) (*UI, *Canvas) {
	env := newEnv(t.TempDir())
	env.Demo, env.Sync, env.DemoNow = true, true, demoNow
	u := NewUI(env, 640, 480)
	if err := demoScene(u, "track", demoTLEs()); err != nil {
		t.Fatal(err)
	}
	return u, NewCanvas(640, 480)
}

// Mash every button on every page, theme and warp setting; nothing may panic.
func TestUIButtonMash(t *testing.T) {
	u, c := demoUI(t)
	keys := []int{BtnUp, BtnDown, BtnLeft, BtnRight, BtnA, BtnB, BtnX, BtnY, BtnL2, BtnR2, BtnSelect, BtnStart}
	for p := 0; p < nPages; p++ {
		u.page = p
		for _, k := range keys {
			u.Key(k)
			u.Tick()
			u.Draw(c)
			if u.modal != modalNone {
				u.Key(BtnB)
				u.Key(BtnB)
				u.modal = modalNone
			}
			u.page = p
		}
	}
	// all the way through warp both ways
	for i := 0; i < 10; i++ {
		u.Key(BtnR2)
		u.Draw(c)
	}
	for i := 0; i < 20; i++ {
		u.Key(BtnL2)
		u.Draw(c)
	}
	u.Key(BtnY)
	if !u.isLive() {
		t.Fatal("Y should return to live")
	}
}

func TestSitePickerAndCustom(t *testing.T) {
	u, c := demoUI(t)
	u.env.Config.Site = nil
	u.modal = modalSites
	u.Draw(c)
	u.Key(BtnDown)
	u.Key(BtnDown)
	u.Key(BtnA)
	if u.site() == nil || u.site().Name != presetSites[1].Name {
		t.Fatalf("site %+v", u.site())
	}
	u.modal = modalSites
	u.siteSel = 0
	u.Key(BtnA)
	if u.modal != modalEdit {
		t.Fatal("custom should open the editor")
	}
	for i := 0; i < 30; i++ {
		u.Key(BtnR1) // clamps at 90
	}
	u.Key(BtnX)
	u.Key(BtnLeft)
	u.Draw(c)
	u.Key(BtnA)
	if s := u.site(); s.Lat != 90 || s.Name != "Custom" {
		t.Fatalf("custom site %+v", s)
	}
	u.ensurePasses(u.now())
	u.page = pgPass
	u.Draw(c) // polar site: may have no passes; must not break
}

func TestNoDataAndDecay(t *testing.T) {
	u, c := demoUI(t)
	u.fleet = nil
	for p := 0; p < nPages; p++ {
		u.page = p
		u.Draw(c)
	}
	// a long-dead element set far from its epoch
	u2, c2 := demoUI(t)
	u2.env.DemoNow = demoNow.Add(5 * 365 * 24 * time.Hour)
	u2.goLive()
	u2.passKey = ""
	for p := 0; p < nPages; p++ {
		u2.page = p
		u2.Tick()
		u2.Draw(c2)
	}
}

func TestFleetDocking(t *testing.T) {
	f := buildFleet(ParseTLEText(demoTLEs()), nil, demoNow)
	if len(f) != 5 || f[0].Short != "ISS" || f[1].Short != "TIANGONG" || f[2].Short != "HUBBLE" {
		t.Fatalf("order: %v %v %v", f[0].Short, f[1].Short, f[2].Short)
	}
	for _, v := range f[3:] {
		if v.Host != "ISS" {
			t.Errorf("%s should be docked at ISS", v.Short)
		}
	}
}

func BenchmarkFrames(b *testing.B) {
	env := newEnv(b.TempDir())
	env.Demo, env.Sync, env.DemoNow = true, true, demoNow
	u := NewUI(env, 640, 480)
	demoScene(u, "track", demoTLEs())
	c := NewCanvas(640, 480)
	u.setWarp(8) // worst case: everything moves every frame
	for i := 0; i < b.N; i++ {
		u.page = i % 3
		env.DemoNow = env.DemoNow.Add(time.Second)
		u.Draw(c)
	}
}

func BenchmarkPasses5Days(b *testing.B) {
	tl := ParseTLEText(demoTLEs())[0]
	for i := 0; i < b.N; i++ {
		PredictPasses(tl, presetSites[0].Geo(), demoNow, 5*24*time.Hour, 40)
	}
}
