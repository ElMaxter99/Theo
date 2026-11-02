// Keeps keyboard focus inside the page when the focused control disappears: after "Unregister" removes a project
// card, or a worktree row goes away, the browser would otherwise drop focus on <body> and a keyboard or screen-reader
// user would start again from the top of the document. Focus then moves to the page heading (h1 has tabIndex=-1).
export function startFocusGuard(root: HTMLElement): () => void {
  let last: Element | null = null;
  const onFocusIn = (e: FocusEvent) => { last = e.target as Element | null; };
  const check = () => {
    if (!last || last.isConnected) return;
    const active = document.activeElement;
    if (active && active !== document.body) { last = active; return; }
    last = null;
    // Do not steal focus from an open modal dialog (it keeps its own focus).
    if (document.querySelector("dialog[open]")) return;
    document.querySelector<HTMLElement>("main h1")?.focus();
  };
  const mo = new MutationObserver(check);
  document.addEventListener("focusin", onFocusIn);
  mo.observe(root, { childList: true, subtree: true });
  return () => { document.removeEventListener("focusin", onFocusIn); mo.disconnect(); };
}
