MIYOO MCTL — a mission-control spacecraft tracker for OnionOS (Miyoo Mini Plus)
===============================================================================
Built by Claude, Anthropic's AI model, for snyderman3000.
v0.1.0

Miyoo mctl follows the International Space Station, China's Tiangong station,
the Hubble Space Telescope and the crew and cargo ships that visit them, on
a retro flight-control-room console. It shows where they are now, their
ground tracks, a 3D globe with the orbit, and when they will pass over you —
including the passes you can actually see with your own eyes.

INSPIRED BY MCTL by Liam Droog — github.com/LiamDroog/mctl
  mctl is a mission-control spacecraft display in Rust for desktops and a
  Raspberry Pi driving a CRT. The idea, the retro console look and the four
  themes come from it, and this app is named after it in tribute.
  Miyoo mctl is an UNOFFICIAL handheld take: not affiliated with or endorsed
  by mctl or its author, and written from scratch in Go (no mctl code). Report
  problems to github.com/snyderman3000/Miyoo-mctl, not to the mctl project.

INSTALL
  1. Copy the "App" folder from this zip to the root of your SD card
     (merge it with the App folder that's already there).
  2. Turn Wi-Fi on and open Apps -> Miyoo mctl.
  3. Pick your city (or enter a latitude/longitude) so it can predict passes.
  It downloads orbital elements from CelesTrak and keeps them on the SD card,
  so it works offline afterwards. Elements refresh every 6 hours when Wi-Fi
  is on. Positions stay good for a few days; past ~3 days the MASTER page
  lights the STALE lamp.

PAGES (L1 / R1)
  TRACK   World map: where the vehicle is, its track for the last half orbit
          and the next one and a half (solid in sunlight, dashed in Earth's
          shadow), the dotted footprint where it's above the horizon, the Sun
          and the night side, your site (diamond). Panels: position, orbit
          (time to the next sunset/sunrise on board) and your next pass.
  MASTER  The globe with the orbit ring (altitude exaggerated x4; B toggles
          true scale), state vector, elements, upcoming events and the
          annunciator lamps. D-pad rotates the globe; A goes back to follow.
  PASS    Every pass over your site for 5 days with a sky plot of the one
          selected. A star means a visible pass: the vehicle is sunlit, your
          sky is dark and it climbs above 10 degrees. "SEE IT" gives the window
          to go outside. A shows visible passes only.
  SYS     Site, theme, scanlines, kiosk timing, element update, help.

BUTTONS
  L1/R1 page        X next vehicle      SELECT theme
  L2/R2 time warp (x10 ... x3600, forwards or backwards)   Y back to live
  START kiosk mode: cycles the pages and keeps the screen on — leave it on
        the desk as a little mission-control display. Any button stops it.
  MENU  quit

SETTINGS FILE
  App/Miyoo-mctl/data/config.json (created on first run):
    "theme"          phosphor, amber, retrowave or modern
    "site"           {"name", "lat", "lon", "alt_m", "timezone"} — timezone is
                     an IANA name like "America/Chicago"
    "scanlines"      true/false
    "kiosk_seconds"  seconds per page in kiosk mode
    "extra_catnrs"   more NORAD catalog numbers to track, e.g. [25338]
                     (low Earth orbit only: period under 225 minutes)

CLOCK
  Predictions need the right time. If the Miyoo's clock is off, Miyoo mctl reads
  the time from CelesTrak's server and corrects itself (SYS shows this).

NOT FOR NAVIGATION
  A hobby display. Positions come from public two-line element sets via SGP4
  and are good to a few km; pass times to a few seconds.

CREDITS
  mctl by Liam Droog (github.com/LiamDroog/mctl): the inspiration and design.
  Orbital elements: CelesTrak (celestrak.org) — thank you, Dr. T.S. Kelso.
  SGP4: after Vallado, Crawford, Hujsak & Kelso, "Revisiting Spacetrack
  Report #3" (2006). Map: Natural Earth (public domain) via world-atlas.
  Font: VT323 (SIL OFL). Go x/image (BSD). CA bundle: Mozilla (MPL 2.0).
  Licences are in the licenses folder. Miyoo mctl is MIT-licensed.
