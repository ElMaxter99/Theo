// The frame of every screen (0.2): a header with the search (command palette) and the operations in progress, and a
// sidebar with Home, the projects (each with its running instances), Problems and Settings. The theme and language
// moved to Settings › Preferences.
import { useBusy } from "../lib/busy.ts";
import { isActiveOp } from "../lib/status.ts";
import { useOps } from "../ops/store.ts";
import { useCallback, useEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import type { Meta } from "../api/types.ts";
import { brand } from "../brand.ts";
import { t } from "../strings.ts";
import { href, useRoute } from "../lib/router.ts";
import { Banner } from "./ui.tsx";
import { OpsIndicator } from "../ops/Operations.tsx";
import { useHealth } from "../lib/health.ts";
import { runningCount, useStatus } from "../lib/statusStore.ts";
import { Icon } from "./Icon.tsx";
import { Modal } from "./Dialog.tsx";
import { Palette, useShortcuts } from "./Palette.tsx";
import { Toasts } from "./Toasts.tsx";
import { startFocusGuard } from "../lib/focusguard.ts";

const isMac = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent);

export function Shell({ meta, onRefresh, children }: { meta: Meta | undefined; onRefresh: () => void; children: ComponentChildren }) {
  const route = useRoute();
  const first = useRef(true);
  const appRef = useRef<HTMLDivElement>(null);
  const [palette, setPalette] = useState(false);
  const [help, setHelp] = useState(false);
  useEffect(() => (appRef.current ? startFocusGuard(appRef.current) : undefined), []);
  const openPalette = useCallback(() => setPalette(true), []);
  const openHelp = useCallback(() => setHelp(true), []);
  useShortcuts(openPalette, openHelp);

  // After a navigation, move focus to the page heading so keyboard and screen-reader users land on the new content.
  const key = [route.name, "project" in route ? route.project : "", "wt" in route ? route.wt : "", "tab" in route ? "" : ""].join("|");
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    const before = document.activeElement;
    const id = window.setTimeout(() => {
      const now = document.activeElement;
      if (now && now !== before && now !== document.body && now.isConnected) return;
      const h = document.querySelector<HTMLElement>("main h1");
      if (h) h.focus();
      else document.getElementById("main")?.focus();
    }, 50);
    return () => clearTimeout(id);
  }, [key]);

  const health = useHealth();
  const healthCount = health.summary?.findings;
  const healthDocker = health.summary?.status === "docker_unavailable";
  const status = useStatus();
  const projects = status.report?.projects ?? [];
  const current = "project" in route ? route.project : "";

  const dockerDown = meta && !meta.docker.available;
  const gitDown = meta && !meta.git.available;
  const working = useBusy() || useOps().some(isActiveOp);
  const inProjects = route.name === "projects" || route.name === "add";

  return (
    <div class="app" ref={appRef}>
      <a class="skip-link" href="#main" onClick={(e) => { e.preventDefault(); document.getElementById("main")?.focus(); }}>{t.app.skip}</a>
      <header class="app-header">
        <a class="brand" href={href.home()}>
          <img src={brand.mark} alt="" width="32" height="32" />
          <span>{t.app.name}</span>
        </a>
        <button type="button" class="search-trigger" onClick={openPalette} aria-keyshortcuts={isMac ? "Meta+K" : "Control+K"} data-testid="open-palette">
          <Icon name="search" />
          <span class="grow">{t.palette.trigger}</span>
          <kbd aria-hidden="true">{isMac ? "⌘K" : "Ctrl K"}</kbd>
        </button>
        <span class="header-spacer" />
        <OpsIndicator />
        {working ? <div class="progress-bar" aria-hidden="true" /> : null}
      </header>
      <nav class="app-nav" aria-label={t.app.nav}>
        <ul>
          <li><a href={href.home()} aria-current={route.name === "home" ? "page" : undefined}><span class="nav-icon" aria-hidden="true">⌂</span> {t.nav.home}</a></li>
          <li>
            <a href={href.projects()} aria-current={inProjects ? "page" : undefined}><span class="nav-icon" aria-hidden="true">▤</span> {t.nav.projects}</a>
            {projects.length > 0 ? (
              <ul class="nav-sub" aria-label={t.nav.projectList}>
                {projects.map((p) => {
                  const n = runningCount(p);
                  return (
                    <li key={p.name}>
                      <a href={href.project(p.name)} aria-current={current === p.name ? "page" : undefined}>
                        <span class="grow">{p.name}</span>
                        <span class="nav-count">{n > 0 ? `${n} ▶` : n}<span class="sr-only"> {t.nav.runningSr(n)}</span></span>
                      </a>
                    </li>
                  );
                })}
              </ul>
            ) : null}
          </li>
          <li>
            <a href={href.problems()} aria-current={route.name === "problems" ? "page" : undefined} data-testid="nav-health">
              <Icon name={healthCount === undefined ? "dot" : healthCount > 0 ? "warn" : healthDocker ? "unknown" : "ok"} />
              <span class="grow">{t.nav.problems}</span>
              {healthCount !== undefined ? <span class="count-pill" data-testid="health-count">{healthCount}<span class="sr-only"> {t.nav.healthPillSr(healthCount, !!healthDocker)}</span></span> : null}
              {healthCount === undefined && health.error ? <span class="sr-only"> {t.reconcile.loadError}</span> : null}
            </a>
          </li>
          <li><a href={href.settings()} aria-current={route.name === "settings" ? "page" : undefined}><span class="nav-icon" aria-hidden="true">⚙</span> {t.nav.settings}</a></li>
        </ul>
        {meta ? <p class="nav-foot">{t.app.version(meta.theo_version, meta.api_version)}<br />{t.app.footerLocal}<br /><button type="button" class="linklike" onClick={openHelp}>{t.shortcuts.link}</button></p> : null}
      </nav>
      <main id="main" class="app-main" tabIndex={-1}>
        {dockerDown ? (
          <Banner tone="warn" title={t.banner.dockerTitle} actions={<button type="button" class="btn sm" onClick={onRefresh}>{t.app.refresh}</button>}>
            <p>{t.banner.dockerBody}</p>
            <p>{meta?.docker.hint ?? t.banner.dockerFix}</p>
            {meta?.docker.error ? <p class="small mono">{meta.docker.error}</p> : null}
          </Banner>
        ) : null}
        {gitDown ? <Banner tone="warn" title={t.banner.gitTitle}><p>{t.banner.gitBody}</p></Banner> : null}
        {children}
      </main>
      <Toasts />
      {palette ? <Palette onClose={() => setPalette(false)} /> : null}
      {help ? (
        <Modal title={t.shortcuts.title} onClose={() => setHelp(false)}>
          <dl class="facts">
            {t.shortcuts.list.map(([k, v]) => <><dt><kbd>{k}</kbd></dt><dd>{v}</dd></>)}
          </dl>
          <div class="dialog-actions"><button type="button" class="btn" autoFocus onClick={() => setHelp(false)}>{t.app.close}</button></div>
        </Modal>
      ) : null}
      <div id="live-region" class="sr-only" aria-live="polite" aria-atomic="true" />
    </div>
  );
}
