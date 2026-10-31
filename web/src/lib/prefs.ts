// View preferences stored only in this browser (localStorage, always in try/catch): density and motion.
// The theme has its own module (theme.ts).
export type Density = "comfortable" | "compact";
export type Motion = "system" | "reduce";

const DENSITY = "theo.density";
const MOTION = "theo.motion";

function read(key: string): string | null {
  try { return localStorage.getItem(key); } catch { return null; }
}
function write(key: string, value: string | null): void {
  try { if (value === null) localStorage.removeItem(key); else localStorage.setItem(key, value); } catch { /* the choice just does not persist */ }
}

export const readDensity = (): Density => (read(DENSITY) === "compact" ? "compact" : "comfortable");
export const readMotion = (): Motion => (read(MOTION) === "reduce" ? "reduce" : "system");

export function applyPrefs(): void {
  const root = document.documentElement;
  if (readDensity() === "compact") root.setAttribute("data-density", "compact"); else root.removeAttribute("data-density");
  if (readMotion() === "reduce") root.setAttribute("data-motion", "reduce"); else root.removeAttribute("data-motion");
}

export function saveDensity(d: Density): void { write(DENSITY, d === "compact" ? "compact" : null); applyPrefs(); }
export function saveMotion(m: Motion): void { write(MOTION, m === "reduce" ? "reduce" : null); applyPrefs(); }
