// Theme preference: "light" (default), "dark" or "system". Stored in localStorage, always wrapped in try/catch.
export type ThemePref = "system" | "light" | "dark";
const KEY = "theo.theme";

export function readTheme(): ThemePref {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "light" || v === "dark" || v === "system") return v;
  } catch { /* storage unavailable: light */ }
  return "light";
}

export function applyTheme(pref: ThemePref): void {
  const root = document.documentElement;
  if (pref === "system") root.removeAttribute("data-theme");
  else root.setAttribute("data-theme", pref);
}

export function saveTheme(pref: ThemePref): void {
  try {
    localStorage.setItem(KEY, pref);
  } catch { /* the choice just does not persist */ }
  applyTheme(pref);
}
