import { useEffect, useRef, useState } from "preact/hooks";
import { startSession, onSessionLost, api } from "./api/client.ts";
import type { SessionProblem } from "./api/client.ts";
import { events } from "./api/events.ts";
import type { Meta } from "./api/types.ts";
import { Shell } from "./components/Shell.tsx";
import { startOpsStore } from "./ops/store.ts";
import { startHealthStore } from "./lib/health.ts";
import { Banner, Loading } from "./components/ui.tsx";
import { useRoute } from "./lib/router.ts";
import { Router } from "./pages/Router.tsx";
import { t } from "./strings.ts";
import { applyTheme, readTheme } from "./lib/theme.ts";
import { applyPrefs } from "./lib/prefs.ts";

type Phase = "checking" | "ok" | SessionProblem;

export function App() {
  const [phase, setPhase] = useState<Phase>("checking");
  const [meta, setMeta] = useState<Meta | undefined>();
  const started = useRef(false);

  useEffect(() => {
    applyTheme(readTheme());
    applyPrefs();
    if (started.current) return;
    started.current = true;
    void startSession().then((r) => setPhase(r));
    return onSessionLost((reason) => setPhase((p) => (p === "ok" || p === "checking" ? reason : p)));
  }, []);

  useEffect(() => {
    if (phase !== "ok") return;
    events.start();
    const stopOps = startOpsStore();
    const stopHealth = startHealthStore();
    const onVis = () => events.focus(document.visibilityState === "visible");
    document.addEventListener("visibilitychange", onVis);
    onVis();
    return () => { events.stop(); stopOps(); stopHealth(); document.removeEventListener("visibilitychange", onVis); };
  }, [phase]);

  const loadMeta = () => { api.get<Meta>("/meta").then(setMeta, () => undefined); };
  useEffect(() => { if (phase === "ok") loadMeta(); }, [phase]);
  useEffect(() => {
    if (phase !== "ok") return;
    return events.on("state.changed", loadMeta);
  }, [phase]);

  if (phase === "checking") {
    return <main id="main" class="app-main"><Loading /></main>;
  }
  if (phase !== "ok") return <Disconnected reason={phase} />;
  return (
    <Shell meta={meta} onRefresh={loadMeta}>
      <Content meta={meta} onRefreshMeta={loadMeta} />
    </Shell>
  );
}

function Content({ meta, onRefreshMeta }: { meta: Meta | undefined; onRefreshMeta: () => void }) {
  const route = useRoute();
  return <Router route={route} meta={meta} onRefreshMeta={onRefreshMeta} />;
}

function Disconnected({ reason }: { reason: SessionProblem }) {
  const gone = reason === "server-gone";
  return (
    <main id="main" class="app-main" tabIndex={-1}>
      <h1>{gone ? t.app.serverGoneTitle : t.app.sessionLostTitle}</h1>
      <Banner tone={gone ? "warn" : "info"} role="alert">
        <p>{gone ? t.app.serverGoneBody : reason === "bad-nonce" ? t.app.sessionLostNonce : t.app.sessionLostBody}</p>
        <p><code>{t.app.sessionLostCommand}</code></p>
      </Banner>
    </main>
  );
}
