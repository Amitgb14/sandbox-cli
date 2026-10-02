"use client";

import { useCallback, useEffect, useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { apiBase, setStoredApiBase, storedApiBase } from "@/lib/constants";
import { reconnect, setApiToken } from "@/lib/api/client";
import { useUi, type SavedConnection } from "@/lib/store";

/**
 * Which daemon this browser is pointed at, and how to point it somewhere else.
 *
 * The active connection lives in the two localStorage keys `client.ts` and
 * `constants.ts` own, not in the zustand store — they are read while a *request*
 * is being built rather than while a component renders, and moving them would
 * put a fetch's base URL behind a React store. The store holds the address book
 * beside them. This hook is the seam between the two, so a switcher does not
 * have to know that.
 */

/** "" is the daemon this Studio was served beside — always offered, never saved. */
export type ConnectionKey = string;

/**
 * Switching writes localStorage, which React cannot see.
 *
 * Without a signal, everything that read the active connection at mount keeps
 * its old answer: the header label and its check mark stay on the machine you
 * just left, and — worse, because it is silent — the sidebar keeps filing
 * recents under the previous daemon's key until a reload. The Settings card only
 * looked right because it updates its own local state inline.
 */
const listeners = new Set<() => void>();

function notifyConnectionChange() {
  listeners.forEach((fn) => fn());
}

export function useActiveConnection(): { key: ConnectionKey; url: string; ready: boolean } {
  // Resolved after mount: localStorage does not exist during a server render,
  // and a first client render that disagreed with the server's is a hydration
  // error on every screen that shows the machine's name.
  const [key, setKey] = useState<ConnectionKey>("");
  const [url, setUrl] = useState("");
  const [ready, setReady] = useState(false);
  useEffect(() => {
    const read = () => {
      setKey(storedApiBase());
      setUrl(apiBase());
      setReady(true);
    };
    read();
    listeners.add(read);
    return () => {
      listeners.delete(read);
    };
  }, []);
  return { key, url, ready };
}

/**
 * Point this browser at another daemon.
 *
 * Everything on screen came from the old one, so the cached answers are now
 * about a different machine: the probe is forgotten and every query refetched
 * rather than leaving one host's runs under another's name.
 */
export function useSwitchConnection() {
  const qc = useQueryClient();
  return useCallback(
    (c: SavedConnection | null) => {
      setStoredApiBase(c?.url ?? "");
      // A token belongs to exactly one machine. Switching to the built-in
      // daemon clears the stored one so the injected token takes over again.
      setApiToken(c?.token ?? "");
      reconnect();
      // Before the refetch, so anything keyed on the active connection — the
      // sidebar's recents above all — is already recording under the machine
      // whose answers are about to arrive.
      notifyConnectionChange();
      qc.invalidateQueries();
    },
    [qc],
  );
}

export type ProbeState = "checking" | "up" | "down";

const PROBE_TIMEOUT_MS = 1500;

/**
 * Whether each saved daemon is answering.
 *
 * `/v1/health` is the one route that needs no token, which is what makes this
 * possible at all: a machine you have not switched to yet is one whose token
 * may be wrong, and a probe that could fail on the credential would report a
 * healthy box as down.
 *
 * **Not-yet-probed is `checking`, never `down`** — the same rule the daemon's
 * own uptime strip keeps, where a bucket with no samples paints as absence
 * rather than as an outage. Reading silence as failure is how a closed laptop
 * becomes an incident.
 */
export function useConnectionHealth(urls: string[]): Record<string, ProbeState> {
  const [state, setState] = useState<Record<string, ProbeState>>({});
  // Joined rather than passed as an array: a new array identity on every render
  // would restart the probes forever.
  const key = urls.join("|");

  useEffect(() => {
    const list = key ? key.split("|") : [];
    if (list.length === 0) return;
    let live = true;
    setState((prev) => {
      const next = { ...prev };
      for (const u of list) if (!next[u]) next[u] = "checking";
      return next;
    });
    const controllers: AbortController[] = [];
    for (const u of list) {
      const ac = new AbortController();
      controllers.push(ac);
      const timer = setTimeout(() => ac.abort(), PROBE_TIMEOUT_MS);
      fetch(`${u.replace(/\/+$/, "")}/v1/health`, { signal: ac.signal })
        .then((r) => {
          if (live) setState((prev) => ({ ...prev, [u]: r.ok ? "up" : "down" }));
        })
        .catch(() => {
          if (live) setState((prev) => ({ ...prev, [u]: "down" }));
        })
        .finally(() => clearTimeout(timer));
    }
    return () => {
      live = false;
      controllers.forEach((c) => c.abort());
    };
  }, [key]);

  return state;
}

/** The saved connections, with the built-in one always first. */
export function useConnections(): SavedConnection[] {
  return useUi((s) => s.connections);
}
