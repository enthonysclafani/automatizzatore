import { useSyncExternalStore } from "react";

// A shared clock so relative times ("in 3h") stay fresh without calling Date.now() during render.
let current = Date.now();
const listeners = new Set<() => void>();
let timer: number | undefined;

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  if (timer === undefined) {
    timer = window.setInterval(() => {
      current = Date.now();
      listeners.forEach((l) => l());
    }, 1000);
  }
  return () => {
    listeners.delete(cb);
    if (listeners.size === 0 && timer !== undefined) {
      window.clearInterval(timer);
      timer = undefined;
    }
  };
}

function getSnapshot(): number {
  return current;
}

/** Current time in ms, updated every second. */
export function useNow(): number {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
