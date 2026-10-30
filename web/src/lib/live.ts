// The single polite live region of the page (view-phase.md section 4): moderate, de-duplicated announcements.
let timer: number | undefined;
let last = "";

export function announce(message: string): void {
  const el = document.getElementById("live-region");
  if (!el) return;
  if (timer !== undefined) clearTimeout(timer);
  // Clearing first makes screen readers announce the same text twice in a row.
  el.textContent = "";
  last = message;
  timer = window.setTimeout(() => {
    el.textContent = last;
    timer = undefined;
  }, 60);
}
