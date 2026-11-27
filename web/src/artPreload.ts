// Fetching pictures before they are on screen, so a page that is about to
// show them (the next "Surprise me" film) has them there with the text.

const ready = new Set<string>();
const pending = new Map<string, Promise<void>>();
// Held so the browser keeps the decoded picture, not only the file.
const held: HTMLImageElement[] = [];

/** Fetches and decodes a picture. Resolves either way; a failed one is simply not ready. */
export function preloadArt(url: string | undefined): Promise<void> {
  if (!url || ready.has(url)) return Promise.resolve();
  let p = pending.get(url);
  if (!p) {
    p = new Promise<void>((done) => {
      const img = new Image();
      const end = (ok: boolean) => {
        pending.delete(url);
        if (ok) {
          ready.add(url);
          held.push(img);
          if (held.length > 24) held.shift();
        }
        done();
      };
      img.onload = () => (img.decode ? img.decode().then(() => end(true), () => end(true)) : end(true));
      img.onerror = () => end(false);
      img.src = url;
    });
    pending.set(url, p);
  }
  return p;
}

/** True once a picture has been preloaded: it can be shown at once, with no fade. */
export function artReady(url: string | undefined) {
  return !!url && ready.has(url);
}

/** Preloads several pictures, waiting no longer than `ms` for them. */
export function preloadWithin(urls: (string | undefined)[], ms: number): Promise<void> {
  return Promise.race([
    Promise.all(urls.map(preloadArt)).then(() => undefined),
    new Promise<void>((r) => setTimeout(r, ms)),
  ]);
}
