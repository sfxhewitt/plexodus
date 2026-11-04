# Working on the TV interface

Two tools make the TV UI (`web/src/tv/`) quick to work on without waiting for
a free TV: a Playwright harness that drives it with arrow keys, and a Vite dev
server the real TVs can load so edits show up on screen as you save.

## Playwright focus tests

`web/e2e/` runs the TV UI in Chromium at 960×540 CSS px and device scale 2,
the same layout the Fire TV Cube and Shield WebViews use for a 1080p screen.
The API is mocked (`e2e/mock.ts`, a fake library of 60 films and 8 shows), so
every run sees the same screens and needs no server.

```
cd web
npm ci
npx playwright install chromium   # once per machine
npm run test:tv                   # everything, ~1 minute
npm run test:tv:ui                # Playwright's UI: step through, see each screen
npx playwright show-report        # screenshots and failures from the last run
```

What it checks:

- **Focus audit** (`e2e/audit.spec.ts`), on Home, a film, a show, a season,
  an episode, both libraries, Search and Settings. From every focusable it
  presses each arrow and fails if focus is lost (lands on `<body>`), is stuck
  while something focusable lies that way (the "Down doesn't leave the hero"
  bug), moves the wrong way, skips a row, or lands somewhere you can't see.
  It also checks that everything on the screen can be reached from where the
  page puts focus.
- **Journeys** (`e2e/journeys.spec.ts`), which do what a person with a
  remote does: walk the home rows, open and leave the sidebar, show to season
  to episode and Back, the library grid and genre picker, and typing on
  the search keyboard. Each one saves screenshots.

Screenshots are in the HTML report. To collect them all in one folder, run
`SCREENS_DIR=e2e/screens npm run test:tv` (PowerShell:
`$env:SCREENS_DIR="e2e/screens"; npm run test:tv`).

CI runs the suite on every pull request (the `tv-ui` job) and uploads the
report and screenshots as the `tv-ui-report` artifact.

**Against the real library.** Point it at a running UI with `E2E_BASE_URL`.
The audit and journeys rely on the fake library's ids, so they skip
themselves there. That mode is for writing your own checks against real data,
for example with `npx playwright codegen`.

**Adding a screen.** Add its path to `screens` in `audit.spec.ts`. Mark a
group's landing item with `data-default`, and the audit and engine will both
respect it. If the screen calls a new endpoint, the test fails and names it,
so add it to `e2e/mock.ts`.

## Live-reloading the TVs from your PC (Josh)

The TV app is a WebView pointed at a server address. Point it at a Vite dev
server on the PC instead and it runs the UI straight from `web/src`, with
`/api` and `/art` proxied to the seedbox. Saving a file updates the TV in
about a second, with no build, release or server update.

1. On the PC, in `web/`:

   ```
   npm ci
   npm run dev:tv
   ```

   This listens on port 5173 on all interfaces and proxies to
   `http://ftl19.seedit4.me:8104` (set in `web/.env.tv`; override with the
   `PLEXODUS_SERVER` environment variable). Vite prints the Network URL, such
   as `http://192.168.0.23:5173`.

2. Allow port 5173 through Windows Firewall. The home network is on the
   **Public** profile, so the rule must include it. In an admin PowerShell:

   ```
   New-NetFirewallRule -DisplayName "Vite dev server (Plexodus TV)" -Direction Inbound -Protocol TCP -LocalPort 5173 -Action Allow -Profile Any
   ```

   To remove it later: `Remove-NetFirewallRule -DisplayName "Vite dev server (Plexodus TV)"`.
   Node may also show its own "allow access" prompt the first time; tick
   both Private and Public.

3. On the TV, open Plexodus, go to Settings, choose **Change server**, and
   enter the PC's address, e.g. `192.168.0.23:5173`. The Connect screen checks
   `/api/status`, which goes through the proxy to the seedbox. Sign in as
   usual (the login cookie is kept for the PC's address).

4. Edit anything in `web/src/tv/` and save. CSS changes apply in place.
   Component changes hot-reload and keep the page you're on. Changes to
   `focus.ts` reload the page.

To go back, use **Change server** again and enter `ftl19.seedit4.me:8104`.

Notes:

- The PC needs a fixed LAN address, or a DHCP reservation in the router, or
  the TV loses it when the address changes.
- Playback works through the dev server too: the app's player gets its stream
  URL from the proxied API, so video goes seedbox → PC → TV. That's fine for
  testing, but for normal watching switch back to the seedbox address.
- Only the web UI hot-reloads. Changes under `android/` still need a new APK.

## Screensaver and trailers

- **Screensaver** (`web/src/tv/Screensaver.tsx`): after the idle time set in
  TV Settings (default 3 minutes, per device in localStorage), it cross-fades
  through `/api/screensaver` (random films and shows with backdrops from the
  user's Home libraries) with a slow drift. OK plays the title on screen; any
  other button, or Back, just wakes the app without moving focus. It never
  starts while the app's player is up. The TV's own system screensaver still
  takes over after its timeout.
- **Trailers** (`web/src/tv/Trailer.tsx`, `ambient.ts`): `/api/items/{id}/trailer`
  asks TMDB for the best official YouTube trailer (cached in memory: a week
  for hits, a day for misses). Pages call `requestTrailer(id, delayMs)`; the
  shared backdrop plays it in a muted-or-not YouTube embed that stays invisible
  until the player reports "playing". The Home hero runs its own. The frame is
  `inert` and focus is pulled back if it ever lands there, so the remote keeps
  driving the page. Trailers need an app build with `canEmbed()` (older builds
  could hand the frame's navigation to the YouTube app); in a desktop browser
  with `?tv=1` they play muted.
- The Playwright mock serves a fake YouTube page that reports "playing", so
  `e2e/ambient.spec.ts` covers both offline.
