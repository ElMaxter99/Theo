// The banner and tag of a new version, and Settings › Updates (board V-17).
import { useState } from "preact/hooks";
import { Badge, Banner, ExternalLink } from "./ui.tsx";
import { Icon } from "./Icon.tsx";
import { announce } from "../lib/live.ts";
import { formatBytes } from "../lib/format.ts";
import { updateActions, useUpdate } from "../lib/update.ts";
import type { UpdateStatus } from "../api/types.ts";
import { href } from "../lib/router.ts";
import { t } from "../strings.ts";

const u = t.update;
const version = (s: UpdateStatus) => s.latest ?? "";

/** Runs one of the buttons; a failure shows as the message of the section. */
function useRun() {
  const [busy, setBusy] = useState(false);
  const run = async (fn: () => Promise<unknown>) => {
    setBusy(true);
    try { await fn(); } catch { /* shown by the store */ } finally { setBusy(false); }
  };
  return { busy, run };
}

/** The banner above the page: a new version is available, or one was just installed. */
export function UpdateBanner() {
  const { status: s } = useUpdate();
  const { busy, run } = useRun();
  if (!s) return null;
  if (s.state === "installed") {
    return (
      <Banner tone="ok" title={u.installedTitle((s.installed ?? "").replace(/^v/, ""))} role="status"
        actions={s.desktop ? <button type="button" class="btn sm primary" disabled={busy} onClick={() => run(updateActions.restart)}>{u.restart}</button> : undefined}>
        <p>{s.desktop ? u.installedHint : u.restartManual}</p>
      </Banner>
    );
  }
  if (!s.available || s.dismissed) return null;
  const title = (s.prerelease ? u.bannerTitleRc : u.bannerTitle)(version(s), s.current);
  const installing = s.state === "downloading";
  return (
    <Banner tone="info" title={title} role="status" actions={
      <span class="row">
        {s.page ? <ExternalLink href={s.page}>{u.news}</ExternalLink> : null}
        <button type="button" class="btn sm" disabled={busy || installing} onClick={() => run(updateActions.dismiss)}>{u.later}</button>
        {s.can_install
          ? <button type="button" class="btn sm primary" disabled={busy || installing} onClick={() => run(updateActions.install)}>{installing ? u.downloading(version(s), "", "") : u.updateNow}</button>
          : <a class="btn sm primary" href={href.settings("updates")}>{u.cannotSelf}</a>}
      </span>
    }>
      <p>{u.bannerHint}</p>
    </Banner>
  );
}

/** The tag next to the search box, while a version is available. */
export function UpdateTag() {
  const { status: s } = useUpdate();
  if (!s || !s.available) return null;
  return <a class="badge info update-tag" href={href.settings("updates")} data-testid="update-tag"><Icon name="info" size={14} /> {u.tag(version(s))}</a>;
}

function when(iso: string | undefined): string {
  if (!iso) return u.neverChecked;
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? u.neverChecked : u.lastCheck(d.toLocaleString());
}

function Progress({ s }: { s: UpdateStatus }) {
  const pct = s.total ? Math.min(100, Math.round(((s.done ?? 0) * 100) / s.total)) : undefined;
  return (
    <div class="stack">
      <p>{u.downloading(version(s), formatBytes(s.done), s.total ? formatBytes(s.total) : "")}</p>
      <progress max={100} value={pct} aria-label={u.downloading(version(s), "", "")} />
      <p class="hint">{u.downloadingHint}</p>
    </div>
  );
}

/** The state of the last attempt, as in the six states of the board. */
function State({ s, failure }: { s: UpdateStatus; failure: string | undefined }) {
  const { busy, run } = useRun();
  if (s.state === "downloading") return <Progress s={s} />;
  if (s.state === "installed") {
    return (
      <div class="stack">
        <Badge view={{ label: u.installedTitle((s.installed ?? "").replace(/^v/, "")), icon: "ok", tone: "ok" }} />
        <p class="hint">{s.desktop ? u.installedHint : u.restartManual}</p>
        {s.desktop ? <div><button type="button" class="btn primary" disabled={busy} onClick={() => run(updateActions.restart)}>{u.restart}</button></div> : null}
      </div>
    );
  }
  if (s.state === "error" || s.error_code) {
    const code = s.error_code;
    const [label, hint] = code === "checksum" ? [u.checksum, u.checksumHint]
      : code === "check_failed" ? [u.offline, u.offlineHint]
      : code === "not_writable" || code === "unmanaged" ? [u.cannotSelf, u.cannotSelfHint]
      : [u.failed, s.error ?? ""];
    return (
      <div class="stack" role="alert">
        <Badge view={{ label, icon: "error", tone: "error" }} />
        <p class="hint">{hint}</p>
        {s.error && code !== "checksum" ? <p class="small mono prewrap">{s.error}</p> : null}
      </div>
    );
  }
  if (failure) return <div class="stack" role="alert"><Badge view={{ label: u.offline, icon: "error", tone: "error" }} /><p class="hint">{u.offlineHint}</p></div>;
  if (s.available) return null;
  return (
    <div class="stack">
      <Badge view={{ label: u.upToDate(s.current), icon: "ok", tone: "ok" }} />
    </div>
  );
}

/** Settings › Updates. */
export function UpdatesSection() {
  const { status: s, failure } = useUpdate();
  const { busy, run } = useRun();
  if (!s) return <section class="card"><p>{u.checking}</p></section>;
  const check = () => run(async () => { await updateActions.check(); announce(u.checking); });
  const save = (change: Parameters<typeof updateActions.settings>[0]) => run(async () => { await updateActions.settings(change); announce(u.saved); });
  const installing = s.state === "downloading";
  const pending = s.available && s.state !== "installed";
  return (
    <>
      <section class="card stack" aria-labelledby="st-update">
        <h2 id="st-update">{u.title}</h2>
        <p>{u.lead}</p>
        <div class="row">
          <strong>{u.installed}</strong>
          <span class="mono">{s.current}</span>
          <Badge view={{ label: s.channel === "rc" ? u.channelRc : u.channelStable, icon: "info", tone: "info" }} />
        </div>
        <p class="hint">{when(s.checked_at)}</p>
        <State s={s} failure={failure} />
        <div class="row">
          {s.available && s.can_install && s.state !== "installed"
            ? <button type="button" class="btn primary" disabled={busy || installing} onClick={() => run(updateActions.install)}>{u.updateTo(version(s))}</button>
            : null}
          {pending && !s.can_install && s.page
            ? <a class="btn primary" href={s.page} target="_blank" rel="noopener noreferrer">{u.downloadsPage}</a>
            : null}
          <button type="button" class="btn" disabled={busy || installing} onClick={check}>{u.checkNow}</button>
          {pending && s.page ? <ExternalLink href={s.page}>{u.releaseNotes}</ExternalLink> : null}
        </div>
        {pending && !s.can_install ? <p class="hint">{u.cannotSelfHint}</p> : null}
        {pending && s.prerelease ? <p class="hint">{u.rcAvailable(version(s))}</p> : null}
      </section>
      <section class="card stack" aria-labelledby="st-update-how">
        <h2 id="st-update-how">{u.howTitle}</h2>
        <div class="check">
          <input id="upd-auto" type="checkbox" checked={s.enabled} disabled={busy} onChange={(e) => { void save({ auto_check: (e.currentTarget as HTMLInputElement).checked }); }} />
          <label for="upd-auto">{u.auto}<span class="hint block">{u.autoHint}</span></label>
        </div>
        <div class="check">
          <input id="upd-notify" type="checkbox" checked={s.notify} disabled={busy || !s.enabled} onChange={(e) => { void save({ notify: (e.currentTarget as HTMLInputElement).checked }); }} />
          <label for="upd-notify">{u.notify}<span class="hint block">{u.notifyHint}</span></label>
        </div>
        <div class="check">
          <input id="upd-rc" type="checkbox" checked={s.channel === "rc"} disabled={busy} onChange={(e) => { void save({ channel: (e.currentTarget as HTMLInputElement).checked ? "rc" : "stable" }); }} />
          <label for="upd-rc">{u.rc} <Badge view={{ label: u.rcBadge, icon: "warn", tone: "warn" }} /><span class="hint block">{u.rcHint}</span></label>
        </div>
      </section>
    </>
  );
}
