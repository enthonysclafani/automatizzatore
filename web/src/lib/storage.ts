// localStorage can throw (private windows, blocked site data); never let it break the app.

export function readStorage(key: string): string | null {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeStorage(key: string, value: string | null): void {
  try {
    if (value === null) window.localStorage.removeItem(key);
    else window.localStorage.setItem(key, value);
  } catch {
    // Ignore: the preference simply is not remembered.
  }
}

export const STORAGE_KEYS = {
  theme: "pendolo.theme",
  workspace: "pendolo.workspace",
  sidebar: "pendolo.sidebar",
} as const;
