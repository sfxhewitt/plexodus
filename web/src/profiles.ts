// Profiles ("Who's watching?"): shared by the web and TV interfaces.
import { api, User } from "./api";

export interface Profile {
  id: number;
  name: string;
  avatar: string;
  color: string;
  maxAge?: number;
  kids: boolean;
  hasPin: boolean;
  /** The account's own (main) profile. */
  isAccount: boolean;
}

export interface RatingLevel {
  maxAge: number;
  label: string;
  detail: string;
}

export interface ProfilesInfo {
  profiles: Profile[];
  /** The profile active on this device. */
  current: number;
  /** False on a profile: only the main profile can add or change profiles. */
  canManage: boolean;
  levels: RatingLevel[];
  max: number;
}

export interface ProfileInput {
  name?: string;
  avatar?: string;
  color?: string;
  kids?: boolean;
  maxAge?: number;
  /** 4 digits; "" removes the PIN. */
  pin?: string;
}

export const AVATARS = ["🦊", "🐼", "🦁", "🐯", "🐸", "🐙", "🦄", "🐲", "🐧", "🐨", "🦉", "🐝", "🚀", "👾", "🤖", "⭐", "🎮", "🍕"];

export const COLORS: Record<string, string> = {
  cyan: "#22d3ee",
  magenta: "#ff2bd6",
  violet: "#8b5cf6",
  blue: "#3b82f6",
  teal: "#2dd4bf",
  lime: "#a3e635",
  amber: "#fbbf24",
  coral: "#fb7185",
};
const COLOR_KEYS = Object.keys(COLORS);

/** A profile's colour, defaulting to one picked from its id. */
export function colorOf(p: { id: number; color?: string }) {
  return COLORS[p.color || ""] || COLORS[COLOR_KEYS[p.id % COLOR_KEYS.length]];
}

export const initialOf = (name?: string) => (name || "?").trim().slice(0, 1).toUpperCase();

/** Whether switching to `p` asks for its PIN (mirrors the server's rule). */
export function needsPin(p: Profile, info: ProfilesInfo) {
  if (!p.hasPin || p.id === info.current) return false;
  const fromMain = info.profiles.find((x) => x.id === info.current)?.isAccount;
  return !(fromMain && !p.isAccount);
}

export const loadProfiles = () => api.get<ProfilesInfo>("/api/profiles");

const PICKED = "plexodus.profilePicked";

/**
 * The picker shows once per app start (per browser tab on the web) when an
 * account has more than one profile.
 */
export function shouldPick(info: ProfilesInfo) {
  try {
    if (sessionStorage.getItem(PICKED)) return false;
  } catch {
    /* storage blocked: ask every time */
  }
  return info.profiles.length > 1;
}

export function markPicked() {
  try {
    sessionStorage.setItem(PICKED, "1");
  } catch {
    /* ignore */
  }
}

/** Makes a profile active on this device; drops every cached page. */
export async function switchProfile(id: number, pin?: string) {
  const u = await api.post<User>(`/api/profiles/${id}/switch`, { pin: pin || "" });
  api.invalidate();
  markPicked();
  return u;
}

export const createProfile = (p: ProfileInput) => api.post<Profile>("/api/profiles", p);
export const updateProfile = (id: number, p: ProfileInput) => api.put(`/api/profiles/${id}`, p);
export const deleteProfile = (id: number) => api.del(`/api/profiles/${id}`);

/** After signing out: ask "Who's watching?" again on the next sign-in. */
export function forgetPicked() {
  try {
    sessionStorage.removeItem(PICKED);
  } catch {
    /* ignore */
  }
}

/** After switching profile, start again from Home. */
export function goHome() {
  try {
    window.history.replaceState(null, "", "/" + window.location.search);
  } catch {
    /* ignore */
  }
}
