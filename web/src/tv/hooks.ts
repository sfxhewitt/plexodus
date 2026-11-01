import { createContext, useContext, useEffect, useRef, useState } from "react";
import { useLocation } from "react-router-dom";
import { api, Library } from "../api";
import { focusEl, focusables, focusInitial } from "./focus";

// ---------- focus memory per history entry ----------

const memory = new Map<string, string>();
let currentKey = "";
export const setCurrentKey = (k: string) => (currentKey = k);

document.addEventListener("focusin", (e) => {
  const el = e.target as HTMLElement;
  if (el.dataset?.fk && !el.closest(".tv-nav") && !el.closest("[data-scope]")) memory.set(currentKey, el.dataset.fk);
});

/** Focuses a page's last-focused item (when coming back) or its starting point, once `ready`. */
export function usePageFocus(ready: boolean) {
  const loc = useLocation();
  const done = useRef<string>();
  useEffect(() => {
    if (!ready || done.current === loc.key) return;
    done.current = loc.key;
    const want = (loc.state as { focus?: string } | null)?.focus || memory.get(loc.key);
    requestAnimationFrame(() => focusInitial(want));
  }, [ready, loc.key, loc.state]);
}

/** Moves focus from the sidebar back into the page, where it was. */
export const focusPage = () => {
  const all = focusables().filter((el) => !el.closest(".tv-nav"));
  const want = memory.get(currentKey);
  focusEl(all.find((el) => el.dataset.fk === want) || all.find((el) => el.hasAttribute("data-autofocus")) || all[0], true);
};

/** Pages call this to refetch after playback ends (watched state, resume points). */
export function useRefresh(reload: () => void) {
  useEffect(() => {
    window.addEventListener("tv:refresh", reload);
    return () => window.removeEventListener("tv:refresh", reload);
  }, [reload]);
}

export const ToastCtx = createContext<(msg: string) => void>(() => {});
export const useToast = () => useContext(ToastCtx);

export function useLibraries() {
  const [data, setData] = useState<Library[] | undefined>(() => api.cached<Library[]>("/api/libraries"));
  useEffect(() => {
    api.get<Library[]>("/api/libraries").then(setData, () => {});
  }, []);
  return { data };
}
