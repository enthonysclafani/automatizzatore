import { useSyncExternalStore } from "react";
import { readStorage, STORAGE_KEYS, writeStorage } from "./storage";

export type ThemeChoice = "system" | "light" | "dark";
export type ResolvedTheme = "light" | "dark";

const listeners = new Set<() => void>();
const media = typeof window !== "undefined" ? window.matchMedia("(prefers-color-scheme: dark)") : undefined;

function storedChoice(): ThemeChoice {
  const v = readStorage(STORAGE_KEYS.theme);
  return v === "light" || v === "dark" ? v : "system";
}

let choice: ThemeChoice = storedChoice();

function apply(): void {
  const root = document.documentElement;
  if (choice === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", choice);
}

function notify(): void {
  listeners.forEach((l) => l());
}

/** Call once at startup, before the first render. */
export function initTheme(): void {
  apply();
  media?.addEventListener("change", notify);
}

export function setTheme(next: ThemeChoice): void {
  choice = next;
  writeStorage(STORAGE_KEYS.theme, next === "system" ? null : next);
  apply();
  notify();
}

function subscribe(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}

function resolved(): ResolvedTheme {
  if (choice !== "system") return choice;
  return media?.matches ? "dark" : "light";
}

export function useThemeChoice(): ThemeChoice {
  return useSyncExternalStore(subscribe, () => choice, () => choice);
}

export function useResolvedTheme(): ResolvedTheme {
  return useSyncExternalStore(subscribe, resolved, resolved);
}
