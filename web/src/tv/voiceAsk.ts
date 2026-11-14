// What a voice request asks for ("Mobland season 2 episode 3", "Dune from the
// start") and how well a search result fits it. No browser APIs, so tests can
// import it directly.
import type { Card } from "../api";

export interface VoiceAsk {
  title: string;
  season?: number;
  episode?: number;
  fromStart: boolean;
}

const WORDS: Record<string, number> = {
  one: 1, two: 2, three: 3, four: 4, five: 5, six: 6, seven: 7, eight: 8, nine: 9, ten: 10, eleven: 11, twelve: 12,
  thirteen: 13, fourteen: 14, fifteen: 15, sixteen: 16, seventeen: 17, eighteen: 18, nineteen: 19, twenty: 20,
  first: 1, second: 2, third: 3, fourth: 4, fifth: 5, sixth: 6, seventh: 7, eighth: 8, ninth: 9, tenth: 10,
};
const num = (w: string) => (/^\d+$/.test(w) ? Number(w) : WORDS[w]);
const N = "(\\d+|[a-z]+)";

/** Splits what was said into the title and any season, episode or "from the start". */
export function parseVoice(said: string, from?: string | null): VoiceAsk {
  let t = ` ${said.toLowerCase().replace(/[’`]/g, "'").replace(/[^a-z0-9' ]+/g, " ")} `.replace(/\s+/g, " ");
  let fromStart = from === "start";
  t = t.replace(/ (from the (start|beginning)|from scratch|right from the start|again) /g, () => ((fromStart = true), " "));
  let season: number | undefined;
  let episode: number | undefined;
  const take = (re: RegExp, f: (m: RegExpMatchArray) => boolean) => {
    const m = t.match(re);
    if (m && f(m)) t = t.replace(m[0], " ");
  };
  take(new RegExp(` (?:season|series) ${N} (?:episode|ep) ${N} `), (m) => {
    season = num(m[1]);
    episode = num(m[2]);
    return season != null && episode != null;
  });
  if (season == null) {
    take(new RegExp(` (?:episode|ep) ${N} (?:of )?(?:season|series) ${N} `), (m) => {
      episode = num(m[1]);
      season = num(m[2]);
      return season != null && episode != null;
    });
  }
  if (season == null) {
    take(/ s ?(\d+) ?e ?(\d+) /, (m) => ((season = Number(m[1])), (episode = Number(m[2])), true));
  }
  if (season == null) take(new RegExp(` (?:season|series) ${N} `), (m) => (season = num(m[1])) != null);
  if (episode == null) take(new RegExp(` (?:episode|ep) ${N} `), (m) => (episode = num(m[1])) != null);
  t = t
    .replace(/^ (the )?(next episode of|film|movie|show|series|programme|tv show|telly|watching) /, " ")
    .replace(/ (please|for me|on (plex|plexodus))? $/, " ")
    .trim();
  return { title: t, season, episode, fromStart };
}

export const norm = (s: string) =>
  s.toLowerCase().normalize("NFKD").replace(/[̀-ͯ]/g, "").replace(/&/g, " and ").replace(/[^a-z0-9]+/g, " ").trim();
const tight = (s: string) => norm(s).replace(/^the /, "").replace(/ /g, "");

/** How well a search result fits the spoken title (0-100). */
export function score(c: Card, title: string) {
  const name = c.kind === "episode" ? c.showTitle || c.title : c.title;
  const a = norm(name);
  const b = norm(title);
  if (!a || !b) return 0;
  let s: number;
  if (a === b || tight(name) === tight(title)) s = 100;
  else if (a.startsWith(b + " ") || tight(name).startsWith(tight(title))) s = 80;
  else if (` ${a} `.includes(` ${b} `)) s = 65;
  else {
    const want = b.split(" ");
    const have = new Set(a.split(" "));
    s = Math.round((40 * want.filter((w) => have.has(w)).length) / want.length);
  }
  if (c.kind === "episode") s -= 10;
  if (c.position) s += 3;
  if (c.viewedLeafCount && c.leafCount && c.viewedLeafCount < c.leafCount) s += 3;
  return s;
}

