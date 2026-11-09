// Classic script run from <head> before first paint, so a forced theme never flashes. No imports on purpose
// (the build emits it as a plain script). Default is light; "system" is an explicit choice.
try {
  const v = localStorage.getItem("theo.theme");
  if (v !== "system") document.documentElement.setAttribute("data-theme", v === "dark" ? "dark" : "light");
} catch {
  document.documentElement.setAttribute("data-theme", "light");
}
