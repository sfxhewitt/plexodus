// Screenshots for the Google Play and Amazon Appstore listings.
//
//   STORE_SHOTS=../docs/store/screenshots npx playwright test store-shots --trace off
//
// Uses the mock library with drawn artwork (no real film art: store listings
// must not show other people's posters). TV shots are 1920x1080 (the TV
// interface at 960x540 CSS px, DPR 2); phone shots 1080x1920.
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import type { Page } from "@playwright/test";
import { test } from "./fixtures";
import { focused, press, waitForFocus } from "./focus";
import { artTitles, ids, MOVIES_LIB } from "./mock";

const OUT = process.env.STORE_SHOTS;
test.skip(!OUT || !!process.env.E2E_BASE_URL, "set STORE_SHOTS=<folder> to make store screenshots");

function rng(seed: string) {
  let h = 2166136261;
  for (let i = 0; i < seed.length; i++) h = Math.imul(h ^ seed.charCodeAt(i), 16777619);
  return () => {
    h = Math.imul(h ^ (h >>> 15), 2246822507);
    h = Math.imul(h ^ (h >>> 13), 3266489909);
    return ((h ^= h >>> 16) >>> 0) / 4294967296;
  };
}

const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/'/g, "&#39;");

/** A drawn poster or backdrop: sky, a sun or moon, layered hills, and the title on posters. */
function art(path: string) {
  const wide = path.includes("/b/");
  const show = path.match(/\/p\/s(\d+)-\d+/);
  const title = artTitles.get(path) || (show ? artTitles.get(`/p/s${show[1]}.jpg`) : undefined) || "";
  const r = rng(title || path);
  const [w, h] = wide ? [1920, 1080] : [684, 1026];
  const hue = Math.floor(r() * 360);
  const hue2 = (hue + 25 + Math.floor(r() * 90)) % 360;
  const sunX = w * (0.2 + r() * 0.6);
  const sunY = h * (wide ? 0.3 + r() * 0.2 : 0.22 + r() * 0.18);
  const sunR = (wide ? h : w) * (0.12 + r() * 0.14);
  const layers = 4;
  let hills = "";
  for (let l = 0; l < layers; l++) {
    const base = h * (0.55 + l * 0.1);
    const amp = h * (0.1 - l * 0.015);
    const pts: string[] = [`0,${h}`];
    const steps = 8 + Math.floor(r() * 6);
    for (let i = 0; i <= steps; i++) {
      const x = (w / steps) * i;
      const y = base - Math.abs(Math.sin(i * (0.6 + r() * 0.8) + l)) * amp - r() * amp * 0.4;
      pts.push(`${x.toFixed(0)},${y.toFixed(0)}`);
    }
    pts.push(`${w},${h}`);
    hills += `<polygon points="${pts.join(" ")}" fill="hsl(${hue2},${45 - l * 6}%,${26 - l * 6}%)"/>`;
  }
  const serif = r() > 0.5;
  const words = title.toUpperCase().split(" ");
  const lines: string[] = [];
  for (const wd of words) {
    const last = lines[lines.length - 1];
    if (last && (last + " " + wd).length <= 11) lines[lines.length - 1] = last + " " + wd;
    else lines.push(wd);
  }
  const size = Math.min(92, Math.floor((w * 0.86) / Math.max(...lines.map((l) => l.length), 4) / 0.62));
  const text = wide || !title
    ? ""
    : lines
        .map((l, i) => `<text x="50%" y="${h * 0.86 - (lines.length - 1 - i) * size * 1.05}" text-anchor="middle" fill="#fff"
          font-family="${serif ? "DejaVu Serif, serif" : "DejaVu Sans, sans-serif"}" font-weight="700" font-size="${size}"
          letter-spacing="${serif ? 2 : 6}" style="paint-order:stroke" stroke="rgba(0,0,0,.25)" stroke-width="3">${esc(l)}</text>`)
        .join("");
  return `<svg xmlns="http://www.w3.org/2000/svg" width="${w}" height="${h}" viewBox="0 0 ${w} ${h}">
<defs>
<linearGradient id="sky" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="hsl(${hue},55%,14%)"/><stop offset=".65" stop-color="hsl(${hue2},60%,42%)"/><stop offset="1" stop-color="hsl(${hue2},70%,62%)"/></linearGradient>
<radialGradient id="sun"><stop offset="0" stop-color="hsl(${(hue2 + 30) % 360},95%,88%)"/><stop offset=".7" stop-color="hsl(${(hue2 + 20) % 360},90%,70%)"/><stop offset="1" stop-color="hsl(${(hue2 + 20) % 360},90%,70%)" stop-opacity="0"/></radialGradient>
<linearGradient id="shade" x1="0" y1="0" x2="0" y2="1"><stop offset=".55" stop-color="#000" stop-opacity="0"/><stop offset="1" stop-color="#000" stop-opacity=".55"/></linearGradient>
</defs>
<rect width="100%" height="100%" fill="url(#sky)"/>
<circle cx="${sunX}" cy="${sunY}" r="${sunR * 1.6}" fill="url(#sun)" opacity=".35"/>
<circle cx="${sunX}" cy="${sunY}" r="${sunR}" fill="url(#sun)"/>
${hills}
<rect width="100%" height="100%" fill="url(#shade)"/>
${text}</svg>`;
}

async function drawnArt(page: Page) {
  // Registered after the mock's own handler, so this one answers.
  await page.route("**/art/**", (route) => {
    const u = new URL(route.request().url());
    return route.fulfill({ status: 200, contentType: "image/svg+xml", body: art(u.pathname.replace(/^\/art\/[^/]+/, "")) });
  });
}

async function shot(page: Page, name: string) {
  await page.waitForLoadState("networkidle").catch(() => {});
  await page.waitForTimeout(900);
  mkdirSync(OUT!, { recursive: true });
  writeFileSync(join(OUT!, `${name}.png`), await page.screenshot());
}

test.describe("TV 1920x1080", () => {
  test.beforeEach(async ({ page }) => drawnArt(page));

  test("tv shots", async ({ page, openTv }) => {
    await openTv("/");
    await waitForFocus(page, "hero-play");
    await shot(page, "tv-1-home");
    await press(page, "down");
    await press(page, "down");
    await shot(page, "tv-2-rows");

    await openTv(`/item/${ids.movie}`);
    await waitForFocus(page, "play");
    await shot(page, "tv-3-movie");

    await openTv(`/item/${ids.show}`);
    await waitForFocus(page, "play");
    await press(page, "down");
    await shot(page, "tv-4-show");
    await page.keyboard.press("Enter");
    await waitForFocus(page, "play");
    let g = await press(page, "down");
    if (g.fk === "show") g = await press(page, "down");
    await press(page, "down");
    await shot(page, "tv-5-episodes");

    await openTv(`/library/${MOVIES_LIB}`);
    await page.waitForTimeout(500);
    await press(page, "down");
    await shot(page, "tv-6-library");
    void focused;
  });
});

test.describe("Phone 1080x1920", () => {
  test.use({ viewport: { width: 360, height: 640 }, deviceScaleFactor: 3, hasTouch: true, isMobile: true });
  test.beforeEach(async ({ page }) => {
    await drawnArt(page);
    await page.addInitScript(() => {
      try {
        localStorage.setItem("plexodus.tv", "0");
        sessionStorage.setItem("plexodus.tv.session", "0");
      } catch {
        /* ignore */
      }
    });
  });

  test("phone shots", async ({ page }) => {
    await page.goto("/?tv=0");
    await shot(page, "phone-1-home");
    await page.goto(`/item/${ids.movie}?tv=0`);
    await shot(page, "phone-2-movie");
    await page.goto(`/item/${ids.show}?tv=0`);
    await shot(page, "phone-3-show");
    await page.goto(`/library/${MOVIES_LIB}?tv=0`);
    await shot(page, "phone-4-library");
  });
});
