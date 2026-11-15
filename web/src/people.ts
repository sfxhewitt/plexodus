import { Card } from "./api";

// Cast member pages, shared by the TV and web interfaces.

export type PersonInfo = {
  id: number; name: string; biography?: string; profile?: string;
  birthday?: string; deathday?: string; birthplace?: string; knownFor?: string;
};
export type Credit = { tmdbId: number; kind: "movie" | "show"; title: string; year?: number; character?: string; poster?: string; episodes?: number };
export type PersonPageData = { person: PersonInfo; inLibrary: Card[]; roles: Record<string, string>; elsewhere: Credit[] };

export const initials = (name: string) => name.split(/\s+/).map((w) => w[0]).slice(0, 2).join("").toUpperCase();

const fmtDate = (d?: string) => {
  if (!d) return "";
  const t = new Date(d + "T12:00:00");
  return isNaN(t.getTime()) ? d : t.toLocaleDateString(undefined, { day: "numeric", month: "long", year: "numeric" });
};

/** Age now, or at death. */
export function personAge(p: PersonInfo) {
  if (!p.birthday) return 0;
  const b = new Date(p.birthday), e = p.deathday ? new Date(p.deathday) : new Date();
  let a = e.getFullYear() - b.getFullYear();
  if (e.getMonth() < b.getMonth() || (e.getMonth() === b.getMonth() && e.getDate() < b.getDate())) a--;
  return a > 0 && a < 130 ? a : 0;
}

export function personFacts(p: PersonInfo) {
  const age = personAge(p);
  return [
    p.birthday ? `Born ${fmtDate(p.birthday)}${!p.deathday && age ? ` (age ${age})` : ""}` : "",
    p.deathday ? `Died ${fmtDate(p.deathday)}${age ? ` (aged ${age})` : ""}` : "",
    p.birthplace || "",
  ].filter(Boolean);
}

