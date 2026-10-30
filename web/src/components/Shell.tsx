import { useEffect, useRef, useState } from "preact/hooks";
import type { ComponentChildren } from "preact";
import type { Meta } from "../api/types.ts";
import { brand } from "../brand.ts";
import { t } from "../strings.ts";
import { href, useRoute } from "../lib/router.ts";
import { readTheme, saveTheme } from "../lib/theme.ts";
import type { ThemePref } from "../lib/theme.ts";
import { Banner } from "./ui.tsx";
import { OpsIndicator } from "../ops/Operations.tsx";
import { useHealth } from "../lib/health.ts";
import { Icon } from "./Icon.tsx";

export function Shell({ meta, onRefresh, children }: { meta: Meta | undefined; onRefresh: () => void; children: ComponentChildren }) {
  const route = useRoute();
  const [theme, setTheme] = useState<ThemePref>(readTheme());
  const first = useRef(true);

  // After a navigation, move focus to the page heading so keyboard and screen-reader users land on the new content.
  useEffect(() => {
    if (first.current) { first.current = false; return; }
    const id = window.setTimeout(() => {
      const h = document.querySelector<HTMLElement>("main h1");
      if (h) h.focus();
      else document.getElementById("main")?.focus();
    }, 50);
    return () => clearTimeout(id);
  }, [route.name, "project" in route ? route.project : "", "wt" in route ? route.wt : ""]);

  useEffect(() => setTheme(readTheme()), [route.name]);

  const changeTheme = (v: string) => {
    const p = (v === "light" || v === "dark" ? v : "system") as ThemePref;
    setTheme(p);
    saveTheme(p);
  };

  const health = useHealth();
  const healthCount = health.summary?.findings;
  const healthDocker = health.summary?.status === "docker_unavailable";

  const dockerDown = meta && !meta.docker.available;
  const gitDown = meta && !meta.git.available;

  return (
    <div class="app">
      <a class="skip-link" href="#main" onClick={(e) => { e.preventDefault(); document.getElementById("main")?.focus(); }}>{t.app.skip}</a>
      <header class="app-header">
        <a class="brand" href={href.projects()}>
          <img src={brand.mark} alt="" width="32" height="32" />
          <span>{t.app.name}</span>
        </a>
        <span class="header-spacer" />
        <OpsIndicator />
        <div class="row">
          <label for="theme-select" class="small">{t.app.theme}</label>
          <select id="theme-select" class="input auto" value={theme} onChange={(e) => changeTheme((e.currentTarget as HTMLSelectElement).value)}>
            <option value="system">{t.app.themeSystem}</option>
            <option value="light">{t.app.themeLight}</option>
            <option value="dark">{t.app.themeDark}</option>
          </select>
        </div>
      </header>
      <nav class="app-nav" aria-label={t.app.nav}>
        <ul>
          <li><a href={href.projects()} aria-current={["projects", "add", "project", "wizard", "detail", "shared"].includes(route.name) ? "page" : undefined}>{t.app.navProjects}</a></li>
          <li>
            <a href={href.health()} aria-current={route.name === "health" ? "page" : undefined} data-testid="nav-health">
              <Icon name={healthCount === undefined ? "dot" : healthCount > 0 ? "warn" : healthDocker ? "unknown" : "ok"} />
              <span>{t.nav.health}</span>
              {healthCount !== undefined ? <span class="count-pill" data-testid="health-count">{healthCount}<span class="sr-only"> {healthCount === 1 ? "finding" : "findings"}{healthDocker ? ", Docker unavailable" : ""}</span></span> : null}
              {healthCount === undefined && health.error ? <span class="sr-only"> {t.reconcile.loadError}</span> : null}
            </a>
          </li>
          <li><a href={href.settings()} aria-current={route.name === "settings" ? "page" : undefined}>{t.nav.settings}</a></li>
        </ul>
        {meta ? <p class="nav-foot">{t.app.version(meta.theo_version, meta.api_version)}<br />{t.app.footerLocal}</p> : null}
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
      <div id="live-region" class="sr-only" aria-live="polite" aria-atomic="true" />
    </div>
  );
}
