// Classic script run from <head> before first paint, so a forced theme never flashes. No imports on purpose
// (the build emits it as a plain script).
try {
  const v = localStorage.getItem("theo.theme");
  if (v === "light" || v === "dark") document.documentElement.setAttribute("data-theme", v);
} catch {
  /* storage unavailable: follow the system */
}
