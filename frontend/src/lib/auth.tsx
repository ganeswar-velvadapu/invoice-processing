"use client";

import { createContext, useCallback, useContext, useEffect, useState } from "react";
import { api, getStoredUser, getToken, post, setSession, type Role, type User } from "./api";

interface AuthCtx {
  user: User | null;
  ready: boolean;
  login: (email: string, password: string) => Promise<void>;
  logout: () => void;
  can: (min: Role) => boolean;
}

const rank: Record<Role, number> = { AP_CLERK: 1, AP_MANAGER: 2, ADMIN: 3 };
const Ctx = createContext<AuthCtx | null>(null);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const stored = getStoredUser();
    if (!getToken() || !stored) {
      setReady(true);
      return;
    }
    setUser(stored);
    // refresh role/active flag from the server
    api<User>("/auth/me")
      .then((u) => {
        setUser(u);
        setSession(getToken(), u);
      })
      .catch(() => setUser(null))
      .finally(() => setReady(true));
  }, []);

  const login = useCallback(async (email: string, password: string) => {
    const r = await post<{ token: string; user: User }>("/auth/login", { email, password });
    setSession(r.token, r.user);
    setUser(r.user);
  }, []);

  const logout = useCallback(() => {
    setSession(null, null);
    setUser(null);
    location.href = "/login";
  }, []);

  const can = useCallback((min: Role) => !!user && rank[user.role] >= rank[min], [user]);

  return <Ctx.Provider value={{ user, ready, login, logout, can }}>{children}</Ctx.Provider>;
}

export function useAuth() {
  const c = useContext(Ctx);
  if (!c) throw new Error("useAuth outside AuthProvider");
  return c;
}
