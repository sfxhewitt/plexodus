// D-pad responsiveness benchmark (opt-in: PERF=1 npm run test:tv -- perf).
//
// Drives Home, a library grid and the tab bar with a remote-like rhythm on a
// throttled CPU (PERF_CPU, default 4x: roughly a Shield-class WebView on a
// desktop), with smooth scrolling left on. Reports, per screen:
//  - key→paint: Event Timing duration of each keydown (handler + render + paint)
//  - long frames: frames over 50 ms while moving, and the median frame time
// Results go to the console and to the test's attachments. Run it against a
// production build for real numbers: `npm run build`, `npx vite preview
// --port 5182`, then E2E_BASE_URL=http://localhost:5182 PERF=1 ...
import { test as base, expect, type Page } from "@playwright/test";
import { mockServer, MOVIES_LIB } from "./mock";

const run = !!process.env.PERF;
const CPU = Number(process.env.PERF_CPU || 4);

base.skip(!run, "set PERF=1 to run the benchmark");
base.describe.configure({ mode: "serial" });

async function instrument(page: Page) {
  await page.evaluate(() => {
    const w = window as unknown as { __perf: { events: number[]; frames: number[]; settle: number[]; on: boolean } };
    w.__perf = { events: [], frames: [], settle: [], on: false };
    // Scroll settle: from a key press until the rows and page stop moving.
    let t0 = 0;
    let lastChange = 0;
    let lastPos = "";
    const flush = () => {
      if (t0 && w.__perf.on) w.__perf.settle.push(Math.max(0, lastChange - t0));
      t0 = 0;
    };
    window.addEventListener("keydown", () => {
      flush();
      t0 = performance.now();
      lastChange = t0;
    }, true);
    (window as unknown as { __perfFlush: () => void }).__perfFlush = flush;
    new PerformanceObserver((list) => {
      for (const e of list.getEntries()) if (w.__perf.on && e.name === "keydown") w.__perf.events.push(e.duration);
    }).observe({ type: "event", buffered: false, durationThreshold: 16 } as PerformanceObserverInit);
    let last = performance.now();
    const tick = (t: number) => {
      if (w.__perf.on) w.__perf.frames.push(t - last);
      const pos = [...document.querySelectorAll<HTMLElement>("[data-hscroll],[data-vscroll]")].map((e) => `${e.scrollLeft},${e.scrollTop}`).join(";");
      if (pos !== lastPos) {
        lastPos = pos;
        lastChange = t;
      }
      last = t;
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  });
}

async function measure(page: Page, keys: string[], gap: number) {
  await page.evaluate(() => ((window as unknown as { __perf: { on: boolean } }).__perf.on = true));
  for (const k of keys) {
    await page.keyboard.press(k);
    await page.waitForTimeout(gap);
  }
  await page.waitForTimeout(600);
  const r = await page.evaluate(() => {
    (window as unknown as { __perfFlush: () => void }).__perfFlush();
    const p = (window as unknown as { __perf: { events: number[]; frames: number[]; settle: number[]; on: boolean } }).__perf;
    p.on = false;
    const out = { events: p.events.slice(), frames: p.frames.slice(), settle: p.settle.filter((x) => x > 0) };
    p.events.length = 0;
    p.frames.length = 0;
    p.settle.length = 0;
    return out;
  });
  const sorted = (a: number[]) => [...a].sort((x, y) => x - y);
  const pct = (a: number[], q: number) => (a.length ? sorted(a)[Math.min(a.length - 1, Math.floor(a.length * q))] : 0);
  return {
    presses: keys.length,
    // Event Timing only reports events slower than 16 ms; faster ones count as 16.
    keyToPaintMedian: Math.round(pct([...r.events, ...Array(Math.max(0, keys.length - r.events.length)).fill(16)], 0.5)),
    keyToPaintP90: Math.round(pct([...r.events, ...Array(Math.max(0, keys.length - r.events.length)).fill(16)], 0.9)),
    keyToPaintMax: Math.round(Math.max(16, ...r.events)),
    // Only presses that scrolled something; the gap between presses caps it.
    scrollSettleMedian: Math.round(pct(r.settle, 0.5)),
    scrollSettleMax: Math.round(Math.max(0, ...r.settle)),
    frameMedian: Math.round(pct(r.frames, 0.5) * 10) / 10,
    longFrames: r.frames.filter((f) => f > 50).length,
    frames: r.frames.length,
  };
}

const rep = (k: string, n: number) => Array(n).fill(k);

base("D-pad benchmark", async ({ page }, info) => {
  base.setTimeout(240_000);
  await mockServer(page);
  await page.addInitScript(() => localStorage.setItem("plexodus.tv", "1"));
  const cdp = await page.context().newCDPSession(page);
  await page.goto("/?tv=1");
  await expect(page.locator('[data-fk="hero-play"]')).toBeFocused({ timeout: 15_000 });
  await page.waitForTimeout(1500);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: CPU });
  await instrument(page);

  const results: Record<string, unknown> = {};
  // Home: into the rows, along one, down through the rest and back to the tab bar.
  results["home: down into rows"] = await measure(page, rep("ArrowDown", 4), 350);
  results["home: along a row (held, 120ms)"] = await measure(page, rep("ArrowRight", 14), 120);
  results["home: along a row (taps, 350ms)"] = await measure(page, rep("ArrowLeft", 8), 350);
  results["home: up to tab bar"] = await measure(page, rep("ArrowUp", 6), 350);
  results["tab bar: across"] = await measure(page, [...rep("ArrowLeft", 1), ...rep("ArrowRight", 3)], 350);

  // Library grid.
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: 1 });
  await page.goto(`/library/${MOVIES_LIB}?tv=1`);
  await page.waitForTimeout(1500);
  await instrument(page);
  await cdp.send("Emulation.setCPUThrottlingRate", { rate: CPU });
  results["library: across the grid"] = await measure(page, rep("ArrowRight", 7), 200);
  results["library: down the grid"] = await measure(page, rep("ArrowDown", 6), 250);

  const text = `CPU ${CPU}x\n` + Object.entries(results).map(([k, v]) => `${k.padEnd(34)} ${JSON.stringify(v)}`).join("\n");
  console.log(text);
  await info.attach("perf.txt", { body: text, contentType: "text/plain" });
});
