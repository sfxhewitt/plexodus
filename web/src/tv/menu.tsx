// Long-press (hold OK) or the remote's Menu key opens a list of things you can
// do with what's focused, like Plex's and Jellyfin's context menus.
import { createContext, KeyboardEvent, ReactNode, useCallback, useContext, useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api, Card, episodeLabel, Library, setLibraryPrefs } from "../api";
import { Check, Eye, Info, Play, Plus, Refresh, Tv, X } from "../components/Icons";
import { focusables, focusEl, focusInitial, onBack, setFocusFallback, swallowSelectUntilRelease } from "./focus";
import { useToast } from "./hooks";
import { playCard, playItem } from "./play";

export interface MenuAction {
  key: string;
  label: string;
  icon?: ReactNode;
  run: () => Promise<unknown> | void;
  /** What's focused may disappear (focus then moves to a neighbour). */
  removes?: boolean;
  /** Toast once it's done; also refreshes what's on screen. */
  done?: string;
}

interface MenuSpec {
  title: string;
  sub?: string;
  actions: MenuAction[];
  from?: HTMLElement | null;
}

const MenuCtx = createContext<(m: MenuSpec) => void>(() => {});
export const useMenu = () => useContext(MenuCtx);

// ---------- long press ----------

const HOLD_MS = 550;
const isSelect = (e: KeyboardEvent) => e.key === "Enter" || e.keyCode === 23;

/**
 * Key handlers for something that does `onSelect` on OK and `onLong` when OK
 * is held (or the remote's Menu key is pressed). OK acts on release, so a
 * hold never also selects.
 */
export function useLongPress(onSelect: () => void, onLong: ((el: HTMLElement) => void) | undefined) {
  const hold = useRef<{ t: ReturnType<typeof setTimeout>; fired: boolean } | null>(null);
  const cancel = () => {
    if (hold.current) clearTimeout(hold.current.t);
    hold.current = null;
  };
  useEffect(() => cancel, []);
  if (!onLong) return {};
  return {
    onKeyDown: (e: KeyboardEvent<HTMLElement>) => {
      if (e.key === "ContextMenu" || e.key === "Menu") {
        e.preventDefault();
        onLong(e.currentTarget);
        return;
      }
      if (!isSelect(e)) return;
      e.preventDefault(); // no click on key down: we decide on release
      if (e.repeat || hold.current) return;
      const el = e.currentTarget;
      const h = {
        fired: false,
        t: setTimeout(() => {
          h.fired = true;
          hold.current = null;
          // The key is still down: keep its repeats and release away from the menu.
          swallowSelectUntilRelease();
          onLong(el);
        }, HOLD_MS),
      };
      hold.current = h;
    },
    onKeyUp: (e: KeyboardEvent<HTMLElement>) => {
      if (!isSelect(e) || !hold.current) return;
      e.preventDefault();
      const fired = hold.current.fired;
      cancel();
      if (!fired) onSelect();
    },
    onBlur: cancel,
    onContextMenu: (e: React.MouseEvent<HTMLElement>) => {
      e.preventDefault();
      onLong(e.currentTarget);
    },
  };
}

// ---------- the menu ----------

/** Keys of the items either side of `el` in its row, nearest first. */
function neighbours(el: HTMLElement | null | undefined) {
  const out: string[] = [];
  if (!el) return out;
  const fkOf = (n: Element | null) => (n as HTMLElement | null)?.dataset?.fk;
  for (let n = el.nextElementSibling; n && out.length < 1; n = n.nextElementSibling) if (fkOf(n)) out.push(fkOf(n)!);
  for (let n = el.previousElementSibling; n && out.length < 2; n = n.previousElementSibling) if (fkOf(n)) out.push(fkOf(n)!);
  return out;
}

function MenuPanel({ m, onPick, onClose }: { m: MenuSpec; onPick: (a: MenuAction) => void; onClose: () => void }) {
  useEffect(() => onBack(() => (onClose(), true)), [onClose]);
  useEffect(() => {
    requestAnimationFrame(() => focusInitial());
  }, []);
  return (
    <div className="tv-modal tv-menu" data-scope onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="tv-modal-panel">
        <div className="tv-menu-head">
          <h2 className="tv-modal-title">{m.title}</h2>
          {m.sub && <div className="tv-muted tv-menu-sub">{m.sub}</div>}
        </div>
        <div className="tv-modal-list" data-vscroll="center">
          {m.actions.map((a, i) => (
            <button key={a.key} data-f data-fk={`menu-${a.key}`} data-autofocus={i === 0 ? "" : undefined}
              className="tv-list-item tv-menu-item" onClick={() => onPick(a)}>
              {a.icon}
              <span>{a.label}</span>
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

export function MenuProvider({ children }: { children: ReactNode }) {
  const toast = useToast();
  const [menu, setMenu] = useState<MenuSpec | null>(null);
  const current = useRef<MenuSpec | null>(null);
  current.current = menu;

  // Close, put focus back where the menu was opened from, then run `after`.
  const close = useCallback((after?: () => void) => {
    const fk = current.current?.from?.dataset.fk;
    setMenu(null);
    requestAnimationFrame(() => {
      const el = fk ? focusables().find((x) => x.dataset.fk === fk) : undefined;
      if (el) focusEl(el, true);
      after?.();
    });
  }, []);
  const dismiss = useCallback(() => close(), [close]);

  const pick = useCallback((a: MenuAction) => {
    const from = current.current?.from;
    close(async () => {
      try {
        if (a.removes) setFocusFallback(neighbours(from));
        await a.run();
        if (a.done) {
          toast(a.done);
          // Pages refetch in place, keeping what's on screen until the answer arrives.
          window.dispatchEvent(new Event("tv:refresh"));
        }
      } catch (e) {
        toast((e as Error).message);
      }
    });
  }, [close, toast]);

  return (
    <MenuCtx.Provider value={setMenu}>
      {children}
      {menu && <MenuPanel m={menu} onPick={pick} onClose={dismiss} />}
    </MenuCtx.Provider>
  );
}

// ---------- what you can do with a card ----------

export interface CardMenuOptions {
  /** The card is on Up Next, so it can be removed from there. */
  continueRow?: boolean;
}

function watchedNow(c: Card) {
  if (c.kind === "show" || c.kind === "season") return !!c.leafCount && c.viewedLeafCount === c.leafCount;
  return !!c.watched;
}

/** Opens the action menu for a card. */
export function useCardMenu() {
  const open = useMenu();
  const nav = useNavigate();
  return useCallback((c: Card, opts: CardMenuOptions = {}, from?: HTMLElement | null) => {
    const playable = c.kind === "movie" || c.kind === "episode";
    const resume = playable && !!c.position && !c.watched;
    const watched = watchedNow(c);
    const acts: MenuAction[] = [];
    if (playable) {
      acts.push({ key: "play", label: resume ? "Resume" : "Play", icon: <Play size={22} />, run: () => playItem(c.id) });
      if (resume) acts.push({ key: "restart", label: "Play from the start", icon: <Refresh size={22} />, run: () => playItem(c.id, { fromStart: true }) });
    } else {
      acts.push({ key: "play", label: c.kind === "show" ? "Play next episode" : "Play", icon: <Play size={22} />, run: () => playCard(c) });
    }
    if (opts.continueRow) {
      acts.push({
        key: "hide-continue", label: "Remove from Up Next", icon: <X size={22} />, removes: true,
        done: "Removed from Up Next", run: () => api.post(`/api/items/${c.id}/continue/hide`),
      });
    }
    if (c.kind === "movie" || c.kind === "show") {
      acts.push(c.watchlisted
        ? { key: "watchlist", label: "Remove from Watchlist", icon: <X size={22} />, removes: true, done: "Removed from your Watchlist",
            run: () => api.post(`/api/items/${c.id}/watchlist`, { on: false }) }
        : { key: "watchlist", label: "Add to Watchlist", icon: <Plus size={22} />, done: "Added to your Watchlist",
            run: () => api.post(`/api/items/${c.id}/watchlist`, { on: true }) });
    }
    acts.push({
      key: "watched", label: watched ? "Mark as unwatched" : "Mark as watched", icon: watched ? <Eye size={22} /> : <Check size={22} />,
      removes: !watched && !!opts.continueRow, done: watched ? "Marked as unwatched" : "Marked as watched",
      run: () => api.post(`/api/items/${c.id}/watched`, { watched: !watched }),
    });
    if (c.kind === "episode" && c.showId) acts.push({ key: "show", label: "Go to show", icon: <Tv size={22} />, run: () => nav(`/item/${c.showId}`) });
    if (c.kind === "episode" && c.parentId) acts.push({ key: "season", label: "Go to season", icon: <Tv size={22} />, run: () => nav(`/item/${c.parentId}`) });
    acts.push({ key: "details", label: c.kind === "episode" ? "Episode details" : "Details", icon: <Info size={22} />, run: () => nav(`/item/${c.id}`) });

    const title = c.kind === "episode" ? c.showTitle || c.title : c.title;
    const sub = c.kind === "episode" ? `${episodeLabel(c)} · ${c.title}` : c.year ? String(c.year) : "";
    open({ title, sub, actions: acts, from });
  }, [open, nav]);
}

/** Opens the menu for a library tab: hide it from the menu or from Home. */
export function useLibraryMenu() {
  const open = useMenu();
  return useCallback((l: Library, from?: HTMLElement | null) => {
    open({
      title: l.name,
      sub: "Library",
      from,
      actions: [
        { key: "hide-nav", label: "Hide from the menu", icon: <X size={22} />, removes: true, done: `${l.name} is hidden. Show it again in Settings.`,
          run: () => setLibraryPrefs(l.id, { hidden: true }) },
        l.hiddenHome
          ? { key: "home", label: "Show on Home", icon: <Check size={22} />, done: `${l.name} is back on Home`, run: () => setLibraryPrefs(l.id, { hiddenHome: false }) }
          : { key: "home", label: "Hide from Home", icon: <X size={22} />, done: `${l.name} is off Home`, run: () => setLibraryPrefs(l.id, { hiddenHome: true }) },
      ],
    });
  }, [open]);
}
