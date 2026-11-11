"use client";

const TOKEN_KEY = "ap.token";
const USER_KEY = "ap.user";

export type Role = "AP_CLERK" | "AP_MANAGER" | "ADMIN";
export interface User {
  user_id: string;
  email: string;
  full_name: string;
  role: Role;
}

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

function safeGet(k: string): string | null {
  try {
    return window.localStorage.getItem(k);
  } catch {
    return null;
  }
}

export function getToken() {
  return typeof window === "undefined" ? null : safeGet(TOKEN_KEY);
}

export function getStoredUser(): User | null {
  const raw = typeof window === "undefined" ? null : safeGet(USER_KEY);
  try {
    return raw ? (JSON.parse(raw) as User) : null;
  } catch {
    return null;
  }
}

export function setSession(token: string | null, user: User | null) {
  try {
    if (token && user) {
      localStorage.setItem(TOKEN_KEY, token);
      localStorage.setItem(USER_KEY, JSON.stringify(user));
    } else {
      localStorage.removeItem(TOKEN_KEY);
      localStorage.removeItem(USER_KEY);
    }
  } catch {
    /* storage unavailable */
  }
}

async function raw(path: string, init: RequestInit = {}): Promise<Response> {
  const headers = new Headers(init.headers);
  const t = getToken();
  if (t) headers.set("Authorization", `Bearer ${t}`);
  if (init.body && !(init.body instanceof FormData) && !headers.has("Content-Type")) {
    headers.set("Content-Type", "application/json");
  }
  const res = await fetch(`/api${path}`, { ...init, headers });
  if (res.status === 401 && !path.startsWith("/auth/login")) {
    setSession(null, null);
    if (!location.pathname.startsWith("/login")) location.href = "/login";
  }
  if (!res.ok) {
    let msg = res.statusText;
    try {
      msg = (await res.json()).error ?? msg;
    } catch {
      /* not json */
    }
    throw new ApiError(res.status, msg);
  }
  return res;
}

export async function api<T = unknown>(path: string, init: RequestInit = {}): Promise<T> {
  const res = await raw(path, init);
  return (await res.json()) as T;
}

export const post = <T = unknown>(path: string, body?: unknown) =>
  api<T>(path, { method: "POST", body: body === undefined ? undefined : JSON.stringify(body) });
export const put = <T = unknown>(path: string, body: unknown) =>
  api<T>(path, { method: "PUT", body: JSON.stringify(body) });
export const patch = <T = unknown>(path: string, body: unknown) =>
  api<T>(path, { method: "PATCH", body: JSON.stringify(body) });

/** Fetches a protected file and returns an object URL (the JWT never goes into a URL). */
export async function fileObjectURL(path: string): Promise<string> {
  const res = await raw(path);
  return URL.createObjectURL(await res.blob());
}

export interface Page<T> {
  items: T[];
  total: number;
  limit: number;
  offset: number;
}
