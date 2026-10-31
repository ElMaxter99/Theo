// Settings and diagnostics (view-phase 2.7): the doctor checks, facts about this installation, a copyable report without
// secrets, and the view preferences (stored only in this browser).
import { useEffect, useState } from "preact/hooks";
import { api } from "../api/client.ts";
import type { Diagnostics, Meta } from "../api/types.ts";
import { Badge, ErrorBanner, Loading, PageHead } from "../components/ui.tsx";
import { Icon } from "../components/Icon.tsx";
import { useResource } from "../lib/hooks.ts";
import { announce } from "../lib/live.ts";
import { reportText } from "../lib/report.ts";
import { readDensity, readMotion, saveDensity, saveMotion } from "../lib/prefs.ts";
import type { Density, Motion } from "../lib/prefs.ts";
import { readTheme, saveTheme } from "../lib/theme.ts";
import type { ThemePref } from "../lib/theme.ts";
import type { StatusView } from "../lib/status.ts";
import { t } from "../strings.ts";
import { setTitle } from "./Router.tsx";

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

export function Settings({ meta }: { meta: Meta | undefined }) {
  const doctor = useResource<Diagnostics>(() => api.get<Diagnostics>("/diagnostics"), []);
  const [theme, setTheme] = useState<ThemePref>(readTheme());
  const [density, setDensity] = useState<Density>(readDensity());
  const [motion, setMotion] = useState<Motion>(readMotion());
  const [shown, setShown] = useState("");
  useEffect(() => setTitle([t.settingsPage.title]), []);
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
      <PageHead title={s.title} />

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

      <section class="card stack" aria-labelledby="st-idle">
        <h2 id="st-idle">{s.idleTitle}</h2>
        <p>{s.idleBody(m?.idle ? Math.round(m.idle.timeout_seconds / 60) : undefined)}</p>
      </section>
    </>
  );
}
