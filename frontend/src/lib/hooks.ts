"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";

/** Loads JSON from the API; `path` null skips. `pollMs` refreshes while `pollWhile(data)` is true. */
export function useApi<T>(path: string | null, opts: { pollMs?: number; pollWhile?: (d: T) => boolean } = {}) {
  const [data, setData] = useState<T | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [loading, setLoading] = useState<boolean>(!!path);
  const [tick, setTick] = useState(0);
  const optsRef = useRef(opts);
  optsRef.current = opts;

  const reload = useCallback(() => setTick((t) => t + 1), []);

  useEffect(() => {
    if (!path) return;
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const load = async () => {
      try {
        const d = await api<T>(path);
        if (cancelled) return;
        setData(d);
        setError(null);
        const o = optsRef.current;
        if (o.pollMs && o.pollWhile?.(d)) timer = setTimeout(load, o.pollMs);
      } catch (e) {
        if (!cancelled) setError(e instanceof Error ? e.message : String(e));
      } finally {
        if (!cancelled) setLoading(false);
      }
    };
    load();
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [path, tick]);

  return { data, error, loading, reload, setData };
}
