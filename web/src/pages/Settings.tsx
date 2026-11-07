// Settings and diagnostics (view-phase 2.7): the doctor checks, facts about this installation, a copyable report without
// secrets, and the view preferences (stored only in this browser).
import { useEffect, useState } from "preact/hooks";
import { api } from "../api/client.ts";
import type { Diagnostics, McpInstall, Meta, ProxyStatus } from "../api/types.ts";
import { Badge, ErrorBanner, ExternalLink, Loading, PageHead } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { useAction, useResource } from "../lib/hooks.ts";
import { announce } from "../lib/live.ts";
import { reportText } from "../lib/report.ts";
import { readDensity, readMotion, saveDensity, saveMotion } from "../lib/prefs.ts";
import type { Density, Motion } from "../lib/prefs.ts";
import { readTheme, saveTheme } from "../lib/theme.ts";
import type { ThemePref } from "../lib/theme.ts";
import type { StatusView } from "../lib/status.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";
import { LanguageSelect } from "../components/LanguageSelect.tsx";
import { CopyCommand, copyText } from "../components/CopyCommand.tsx";
import { qs } from "../api/client.ts";
import { href, SETTINGS_SECTIONS } from "../lib/router.ts";
import type { SettingsSection } from "../lib/router.ts";

export function checkStatusView(status: string): StatusView {
  const label = t.settingsPage.status[status] ?? status;
  switch (status) {
    case "ok": return { label, icon: "ok", tone: "ok" };
    case "warn": return { label, icon: "warn", tone: "warn" };
    case "fail": return { label, icon: "error", tone: "error" };
    case "info": return { label, icon: "info", tone: "info" };
    default: return { label, icon: "unknown", tone: "neutral" };
  }
}

// V-09 Settings, in five sections with their own address: Preferences, Local domains, AI agents, Diagnostics, About.
export function Settings({ meta, section }: { meta: Meta | undefined; section: SettingsSection }) {
  useEffect(() => setTitle([t.settingsNav[section], t.settingsPage.title]), [section]);
  return (
    <>
      <PageHead title={t.settingsPage.title} />
      <div class="settings-layout">
        <nav class="subnav vertical" aria-label={t.settingsPage.title}>
          <ul>
            {SETTINGS_SECTIONS.map((id) => <li key={id}><a href={href.settings(id)} aria-current={id === section ? "page" : undefined}>{t.settingsNav[id]}</a></li>)}
          </ul>
        </nav>
        <div class="stack grow">
          {section === "preferences" ? <Preferences /> : null}
          {section === "domains" ? <ProxyCard /> : null}
          {section === "agents" ? <Agents /> : null}
          {section === "diagnostics" ? <DiagnosticsSection meta={meta} /> : null}
          {section === "about" ? <About meta={meta} /> : null}
        </div>
      </div>
    </>
  );
}

function DiagnosticsSection({ meta }: { meta: Meta | undefined }) {
  const doctor = useResource<Diagnostics>(() => api.get<Diagnostics>("/diagnostics"), []);
  const [shown, setShown] = useState("");
  const s = t.settingsPage;
  const diag = doctor.data;
  const d = diag?.doctor;
  const m = diag?.meta ?? meta;

  const copy = async () => {
    const text = diag?.report || reportText(m, d);
    try {
      await navigator.clipboard.writeText(text);
      announce(s.copied);
      setShown("");
    } catch {
      setShown(text);
      announce(s.copyFailed);
    }
  };

  return (
    <>
      <section class="card stack" aria-labelledby="st-doctor">
        <div class="row between">
          <h2 id="st-doctor">{s.doctorTitle}</h2>
          <button type="button" class="btn sm" disabled={doctor.loading} onClick={doctor.reload}><Icon name="restart" /> {s.runAgain}</button>
        </div>
        <p>{s.doctorIntro}</p>
        {doctor.loading && !d ? <Loading /> : null}
        <ErrorBanner error={doctor.error} title={s.loadError} />
        {d ? (
          <>
            <p><span class={`badge ${d.ok ? "ok" : "error"}`}><Icon name={d.ok ? "ok" : "error"} /> <span>{s.overall(d.ok)}</span></span></p>
            <div class="table-wrap" tabIndex={0} role="region" aria-label={s.doctorTitle}>
              <table class="data">
                <caption class="sr-only">{s.doctorTitle}</caption>
                <thead><tr><th scope="col">{s.colCheck}</th><th scope="col">{s.colStatus}</th><th scope="col">{s.colDetail}</th><th scope="col">{s.colFix}</th></tr></thead>
                <tbody>
                  {d.checks.map((c) => (
                    <tr key={c.id}>
                      <th scope="row">{c.name}</th>
                      <td><Badge view={checkStatusView(c.status)} /></td>
                      <td>{c.detail ?? "—"}</td>
                      <td>{c.fix ?? "—"}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </>
        ) : null}
      </section>

      <section class="card stack" aria-labelledby="st-report">
        <h2 id="st-report">{s.copyTitle}</h2>
        <p>{s.copyBody}</p>
        <div><button type="button" class="btn" onClick={() => void copy()}>{s.copyButton}</button></div>
        {shown ? (
          <div class="field">
            <label for="st-report-text">{s.reportLabel}</label>
            <textarea id="st-report-text" class="input report-text" readOnly value={shown} onFocus={(e) => (e.currentTarget as HTMLTextAreaElement).select()} />
          </div>
        ) : null}
      </section>
    </>
  );
}

function About({ meta }: { meta: Meta | undefined }) {
  const s = t.settingsPage;
  const doctor = useResource<Diagnostics>(() => api.get<Diagnostics>("/diagnostics"), []);
  const diag = doctor.data;
  const d = diag?.doctor;
  const m = diag?.meta ?? meta;
  return (
    <>
      <section class="card stack" aria-labelledby="st-about">
        <h2 id="st-about">{s.dataTitle}</h2>
        <dl class="facts">
          <dt>{s.theoVersion}</dt><dd>{m?.theo_version ?? "—"}</dd>
          <dt>{s.apiVersion}</dt><dd>{m?.api_version ?? "—"}</dd>
          <dt>{s.platform}</dt><dd>{d?.platform ?? (m?.os ? `${m.os}/${m.arch ?? ""}` : "—")}</dd>
          <dt>{s.dockerVersion}</dt><dd>{m ? (m.docker.available ? m.docker.version ?? "—" : s.notAvailable) : "—"}</dd>
          <dt>{s.composeVersion}</dt><dd>{m?.compose ? (m.compose.available ? `${m.compose.version ?? "—"}` : s.notAvailable) + (m.compose.min_version ? ` ${s.needs(m.compose.min_version)}` : "") : "—"}</dd>
          <dt>{s.gitVersion}</dt><dd>{m ? (m.git.available ? m.git.version ?? "—" : s.notAvailable) + (m.git.min_version ? ` ${s.needs(m.git.min_version)}` : "") : "—"}</dd>
          <dt>{s.projects}</dt><dd>{diag?.projects ?? "—"}</dd>
          <dt>{s.dataDir}</dt><dd class="mono">{d?.data_dir ?? m?.data_dir ?? "—"}</dd>
        </dl>
      </section>

      <section class="card stack" aria-labelledby="st-idle">
        <h2 id="st-idle">{s.idleTitle}</h2>
        <p>{s.idleBody(m?.idle ? Math.round(m.idle.timeout_seconds / 60) : undefined)}</p>
      </section>
    </>
  );
}

function Preferences() {
  const s = t.settingsPage;
  const [theme, setTheme] = useState<ThemePref>(readTheme());
  const [density, setDensity] = useState<Density>(readDensity());
  const [motion, setMotion] = useState<Motion>(readMotion());
  return (
    <>
      <section class="card stack" aria-labelledby="st-prefs">
        <h2 id="st-prefs">{s.prefsTitle}</h2>
        <div class="field">
          <label for="pref-theme">{t.app.theme}</label>
          <select id="pref-theme" class="input auto" value={theme} onChange={(e) => { const v = (e.currentTarget as HTMLSelectElement).value; const p = (v === "light" || v === "dark" ? v : "system") as ThemePref; setTheme(p); saveTheme(p); }}>
            <option value="system">{t.app.themeSystem}</option>
            <option value="light">{t.app.themeLight}</option>
            <option value="dark">{t.app.themeDark}</option>
          </select>
        </div>
        <div class="field">
          <label for="pref-lang">{t.app.language}</label>
          <LanguageSelect id="pref-lang" describedBy="pref-lang-hint" />
          <p class="hint" id="pref-lang-hint">{t.app.languageHint}</p>
        </div>
        <div class="field">
          <label for="pref-density">{s.density}</label>
          <select id="pref-density" class="input auto" value={density} onChange={(e) => { const v = (e.currentTarget as HTMLSelectElement).value === "compact" ? "compact" : "comfortable"; setDensity(v); saveDensity(v); }}>
            <option value="comfortable">{s.densityComfortable}</option>
            <option value="compact">{s.densityCompact}</option>
          </select>
        </div>
        <div class="field">
          <label for="pref-motion">{s.motion}</label>
          <select id="pref-motion" class="input auto" value={motion} onChange={(e) => { const v = (e.currentTarget as HTMLSelectElement).value === "reduce" ? "reduce" : "system"; setMotion(v); saveMotion(v); }}>
            <option value="system">{s.motionSystem}</option>
            <option value="reduce">{s.motionReduce}</option>
          </select>
        </div>
        <p class="hint">{s.prefsNote}</p>
      </section>
    </>
  );
}

/** AI agents: how to register Theo's MCP server with Claude Code and other clients, and the AGENTS.md paragraph. */
function Agents() {
  const a = t.agents;
  const [owner, setOwner] = useState("");
  const [readOnly, setReadOnly] = useState(false);
  const [applied, setApplied] = useState({ owner: "", readOnly: false });
  const res = useResource<McpInstall>(() => api.get<McpInstall>(`/mcp/install${qs({ owner: applied.owner || undefined, read_only: applied.readOnly || undefined })}`), [applied.owner, applied.readOnly]);
  const doc = res.data;
  const copy = (text: string) => { void copyText(text).then((ok) => announce(ok ? t.copy.copied : t.copy.failed)); };
  const json = doc ? JSON.stringify({ mcpServers: doc.mcpServers }, null, 2) : "";
  return (
    <section class="card stack" aria-labelledby="st-agents">
      <h2 id="st-agents">{a.title}</h2>
      <p>{a.lead}</p>
      <form class="row controls" onSubmit={(e) => { e.preventDefault(); setApplied({ owner: owner.trim(), readOnly }); }}>
        <div class="field">
          <label for="ag-owner">{a.owner}</label>
          <input id="ag-owner" class="input auto" value={owner} aria-describedby="ag-owner-hint" onInput={(e) => setOwner((e.currentTarget as HTMLInputElement).value)} />
        </div>
        <div class="check">
          <input id="ag-ro" type="checkbox" checked={readOnly} onChange={(e) => setReadOnly((e.currentTarget as HTMLInputElement).checked)} />
          <label for="ag-ro">{a.readOnly}</label>
        </div>
        <button type="submit" class="btn sm">{a.update}</button>
      </form>
      <p class="hint" id="ag-owner-hint">{a.ownerHint}</p>
      {res.loading && !doc ? <Loading /> : null}
      <ErrorBanner error={res.error} />
      {doc ? (
        <>
          <h3>{a.claudeCode}</h3>
          <pre class="snippet mono small" tabIndex={0}>{doc.claude_code_command}</pre>
          <div><CopyCommand command={doc.claude_code_command} small={false} /></div>
          <h3>{a.others}</h3>
          <pre class="snippet mono small" tabIndex={0}>{json}</pre>
          <div><button type="button" class="btn" onClick={() => copy(json)}>{a.copyJson}</button></div>
          <h3>{a.snippet}</h3>
          <p class="hint">{a.snippetHint}</p>
          <pre class="snippet mono small" tabIndex={0}>{doc.snippet}</pre>
          <div><button type="button" class="btn" onClick={() => copy(doc.snippet)}>{a.copySnippet}</button></div>
        </>
      ) : null}
    </section>
  );
}

/** The switch of the interface's own `theo proxy` (GET/POST /proxy), with HTTPS (board V-11, ADR-0015). */
function ProxyCard() {
  const s = t.settingsPage;
  const res = useResource<ProxyStatus>(() => api.get<ProxyStatus>("/proxy"), []);
  const [port, setPort] = useState("");
  const [httpsPort, setHttpsPort] = useState("");
  const [pending, setPending] = useState<boolean | null>(null);
  const [httpsPending, setHttpsPending] = useState<boolean | null>(null);
  const st = res.data;
  const startBody = (https: boolean) => {
    const n = Number.parseInt(port || String(st?.port ?? 7080), 10);
    const h = Number.parseInt(httpsPort || String(st?.https_port ?? 7443), 10);
    return { port: Number.isFinite(n) ? n : 7080, https, https_port: Number.isFinite(h) ? h : 7443 };
  };
  const toggle = useAction(async (on: boolean) => {
    if (on) {
      await api.post<ProxyStatus>("/proxy/start", startBody(httpsPending ?? st?.https ?? false));
      announce(s.proxyStarted);
    } else {
      await api.post<ProxyStatus>("/proxy/stop", {});
      announce(s.proxyStoppedMsg);
    }
    res.reload();
  });
  // While the interface's proxy runs, HTTPS is applied by starting it again.
  const toggleHttps = useAction(async (on: boolean) => {
    if (st?.running && st.managed) {
      await api.post<ProxyStatus>("/proxy/stop", {});
      await api.post<ProxyStatus>("/proxy/start", startBody(on));
      announce(s.httpsApplied);
    }
    res.reload();
  });
  const trust = useAction(async (on: boolean) => {
    await api.post<ProxyStatus>(on ? "/proxy/trust" : "/proxy/untrust", {});
    announce(on ? s.caTrustedMsg : s.caUntrustedMsg);
    res.reload();
  });
  const external = !!st?.running && !st.managed;
  const httpsOn = httpsPending ?? st?.https ?? false;
  const ca = st?.authority;
  return (
    <section class="card stack" aria-labelledby="st-proxy">
      <h2 id="st-proxy">{s.proxyTitle}</h2>
      <p>{s.proxyIntro}</p>
      {res.loading && !st ? <Loading /> : null}
      <ErrorBanner error={res.error} />
      {st ? (
        <>
          <div class="check">
            <input id="proxy-on" type="checkbox" checked={pending ?? st.running} disabled={toggle.busy || external}
              aria-describedby="proxy-state proxy-hint" onChange={(e) => {
                const on = (e.currentTarget as HTMLInputElement).checked;
                setPending(on);
                void toggle.run(on).finally(() => setPending(null));
              }} />
            <div>
              <label for="proxy-on">{s.proxySwitch}</label>
              <p class="hint" id="proxy-hint">{s.proxyRemember}</p>
            </div>
          </div>
          {!st.running ? (
            <div class="field">
              <label for="proxy-port">{s.proxyPort}</label>
              <input id="proxy-port" class="input auto" inputMode="numeric" size={6} value={port} placeholder={String(st.port)}
                onInput={(e) => setPort((e.currentTarget as HTMLInputElement).value.replace(/[^0-9]/g, ""))} />
            </div>
          ) : null}
          <p id="proxy-state" role="status">
            {st.running ? (external ? s.proxyExternal(st.port, st.pid ?? 0) : s.proxyRunning(st.port)) : s.proxyStopped}
            {st.running && st.https ? <> {s.httpsOn(st.https_port)}</> : null}
            {st.running && st.url ? <> <ExternalLink href={st.url} label={s.proxyRoutes}>{s.proxyRoutes}</ExternalLink></> : null}
          </p>
          <ErrorBanner error={toggle.error} />

          <h3 class="https-title">{s.httpsTitle}</h3>
          <div class="check">
            <input id="proxy-https" type="checkbox" checked={httpsOn} disabled={toggleHttps.busy || toggle.busy || external}
              aria-describedby="proxy-https-hint" onChange={(e) => {
                const on = (e.currentTarget as HTMLInputElement).checked;
                setHttpsPending(on);
                void toggleHttps.run(on).finally(() => { if (st.running) setHttpsPending(null); });
              }} />
            <div>
              <label for="proxy-https">{s.httpsSwitch}</label>{" "}
              <input id="proxy-https-port" class="input auto" inputMode="numeric" size={5} aria-label={s.httpsPort}
                value={httpsPort} placeholder={String(st.https_port || 7443)} disabled={st.running}
                onInput={(e) => setHttpsPort((e.currentTarget as HTMLInputElement).value.replace(/[^0-9]/g, ""))} />
              <p class="hint" id="proxy-https-hint">{s.httpsWhy}</p>
            </div>
          </div>
          <ErrorBanner error={toggleHttps.error} />
          {httpsOn || ca ? <AuthorityBox ca={ca} trust={trust} /> : null}
          {httpsOn ? <p class="note warn-note"><Icon name="warn" /> <span>{s.httpsAddress}</span></p> : null}
        </>
      ) : null}
    </section>
  );
}

type TrustAction = { busy: boolean; error: Parameters<typeof ErrorBanner>[0]["error"]; run: (on: boolean) => Promise<unknown> };

function AuthorityBox({ ca, trust }: { ca?: ProxyStatus["authority"]; trust: TrustAction }) {
  const s = t.settingsPage;
  if (!ca) return <p class="hint">{s.caNone}</p>;
  const created = new Date(ca.created).toLocaleDateString(undefined, { dateStyle: "medium" });
  return (
    <div class="authority stack">
      <div class="authority-head">
        <div>
          <strong>{s.caTitle}</strong>
          <p class="hint">{s.caFacts(created)}</p>
        </div>
        {ca.trusted
          ? <span class="badge ok"><Icon name="ok" /><span>{s.caTrusted}</span></span>
          : <span class="badge warn"><Icon name="warn" /><span>{s.caNotTrusted}</span></span>}
      </div>
      {ca.can_trust_here ? (
        <div class="row">
          {ca.trusted
            ? <button type="button" class="btn sm" disabled={trust.busy} onClick={() => void trust.run(false)}>{s.caUntrust}</button>
            : <button type="button" class="btn sm primary" disabled={trust.busy} onClick={() => void trust.run(true)}>{s.caTrust}</button>}
          {!ca.trusted ? <span class="hint">{s.caTrustHint}</span> : null}
        </div>
      ) : !ca.trusted ? (
        <div class="row">
          <span class="hint">{s.caTerminal}</span>
          <code>{ca.command}</code>
          <CopyCommand command={ca.command} />
        </div>
      ) : null}
      {ca.trust.nss && ca.trust.nss !== "trusted" ? <p class="hint">{s.caNss}</p> : null}
      <p class="hint">{s.caFirefox} <code>{ca.path}</code></p>
      <ErrorBanner error={trust.error} />
    </div>
  );
}
