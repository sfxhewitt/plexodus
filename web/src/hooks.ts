import { createContext, useCallback, useContext, useEffect, useRef, useState } from "react";
import { api, User } from "./api";

/** Fetches a URL, showing any cached copy immediately while revalidating. */
export function useApi<T>(url: string | null) {
  const [data, setData] = useState<T | undefined>(() => (url ? api.cached<T>(url) : undefined));
  const [error, setError] = useState<string>();
  const [tick, setTick] = useState(0);
  const current = useRef(url);

  useEffect(() => {
    current.current = url;
    if (!url) return;
    const cached = api.cached<T>(url);
    if (cached) setData(cached);
    else setData(undefined);
    setError(undefined);
    api.get<T>(url).then(
      (d) => current.current === url && setData(d),
      (e: Error) => current.current === url && setError(e.message),
    );
  }, [url, tick]);

  const reload = useCallback(() => setTick((t) => t + 1), []);
  return { data, error, reload, setData };
}

export function useDebounced<T>(value: T, ms: number) {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}

export const UserContext = createContext<{ user: User | null; setUser: (u: User | null) => void }>({
  user: null,
  setUser: () => {},
});

export const useUser = () => useContext(UserContext);
