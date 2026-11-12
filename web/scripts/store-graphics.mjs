// Store graphics for Google Play and the Amazon Appstore, drawn from the app's
// own logo and colours.
//
//   node scripts/store-graphics.mjs ../docs/store/graphics
//
// Needs Playwright's Chromium (already a dev dependency for the TV tests).
import { mkdirSync } from "node:fs";
import { join } from "node:path";
import { chromium } from "@playwright/test";

const out = process.argv[2] || "store-graphics";
mkdirSync(out, { recursive: true });

const GRAD = `<linearGradient id="g" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#5eead4"/><stop offset=".55" stop-color="#818cf8"/><stop offset="1" stop-color="#f472b6"/></linearGradient>`;
const MARK = `<path d="M22 16h14a14 14 0 0 1 0 28h-6v6h-8z M30 24v12h6a6 6 0 0 0 0-12z" fill="url(#g)" fill-rule="evenodd"/>`;
const logo = (size, rounded = true) =>
  `<svg width="${size}" height="${size}" viewBox="0 0 64 64"><defs>${GRAD}</defs>${rounded ? `<rect width="64" height="64" rx="16" fill="#0b0d14"/>` : ""}${MARK}</svg>`;

const page = (w, h, body) => `<!doctype html><html><head>
<link href="https://fonts.googleapis.com/css2?family=Inter:wght@500;700;800&display=block" rel="stylesheet">
<style>
*{margin:0;box-sizing:border-box}
html,body{width:${w}px;height:${h}px;overflow:hidden;background:#05060a;color:#fff;font-family:Inter,"DejaVu Sans",sans-serif}
.aurora{position:absolute;inset:0;background:
 radial-gradient(60% 80% at 15% 20%,rgba(94,234,212,.28),transparent 60%),
 radial-gradient(55% 75% at 85% 30%,rgba(244,114,182,.26),transparent 60%),
 radial-gradient(70% 90% at 50% 110%,rgba(129,140,248,.35),transparent 60%),#05060a}
.word{font-weight:800;letter-spacing:-.02em}
.word b{background:linear-gradient(90deg,#5eead4,#818cf8,#f472b6);-webkit-background-clip:text;color:transparent}
.center{position:absolute;inset:0;display:flex;align-items:center;justify-content:center}
</style></head><body><div class="aurora"></div>${body}</body></html>`;

const graphics = {
  // Play: hi-res icon. Amazon: large icon. Full-bleed square; the stores round it.
  "icon-512.png": [512, 512, `<div class="center" style="background:#0b0d14">${logo(512, false)}</div>`],
  // Amazon: small icon.
  "icon-114.png": [114, 114, `<div class="center" style="background:#0b0d14">${logo(114, false)}</div>`],
  // Play: feature graphic.
  "feature-graphic-1024x500.png": [1024, 500, `
    <div class="center" style="gap:34px;flex-direction:column">
      <div style="display:flex;align-items:center;gap:26px">${logo(118)}<div class="word" style="font-size:104px">plex<b>odus</b></div></div>
      <div style="font-size:30px;font-weight:500;color:rgba(255,255,255,.82)">Your own films and shows. Played as they are.</div>
    </div>`],
  // Play (Android TV) and the TV launcher banner.
  "tv-banner-1280x720.png": [1280, 720, `
    <div class="center" style="gap:34px">${logo(190)}<div class="word" style="font-size:150px">plex<b>odus</b></div></div>`],
  // Amazon Fire TV: background image (no text near the edges).
  "firetv-background-1920x1080.png": [1920, 1080, `
    <div class="center" style="opacity:.16">${logo(760, false)}</div>`],
};

const browser = await chromium.launch();
for (const [name, [w, h, body]] of Object.entries(graphics)) {
  const p = await browser.newPage({ viewport: { width: w, height: h }, deviceScaleFactor: 1 });
  await p.setContent(page(w, h, body), { waitUntil: "networkidle" }).catch(() => {});
  await p.evaluate(() => document.fonts.ready);
  await p.screenshot({ path: join(out, name) });
  await p.close();
  console.log(name);
}
await browser.close();
