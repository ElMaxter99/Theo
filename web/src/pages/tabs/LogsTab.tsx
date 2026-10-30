import { useState } from "preact/hooks";
import { api, BASE, qs, wtPath } from "../../api/client.ts";
import type { Services, WorktreeDetail } from "../../api/types.ts";
import { Banner } from "../../components/ui.tsx";
import { Icon } from "../../components/Icon.tsx";
import { VirtualLog } from "../../components/VirtualLog.tsx";
import { useResource } from "../../lib/hooks.ts";
import { useLogStream } from "../../lib/useLogStream.ts";
import { t } from "../../strings.ts";

const TAILS = [200, 1000, 10000, 50000];

export function LogsTab({ project, w }: { project: string; w: WorktreeDetail }) {
  const svc = useResource<Services>(() => api.get<Services>(`${wtPath(project, w.id)}/services`), [project, w.id]);
  const [service, setService] = useState("");
  const [tail, setTail] = useState(200);
  const [follow, setFollow] = useState(true);
  const [search, setSearch] = useState("");

  const path = `${wtPath(project, w.id)}/logs${qs({ service: service ? [service] : undefined, tail, follow: true })}`;
  const resumePath = `${wtPath(project, w.id)}/logs${qs({ service: service ? [service] : undefined, tail: 0, follow: true })}`;
  const s = useLogStream({ path, resumePath, enabled: true });
  const download = `${BASE}${wtPath(project, w.id)}/logs${qs({ service: service ? [service] : undefined, tail: "all", follow: false })}`;

  const statusText =
    s.status === "live" ? t.logs.live :
    s.status === "connecting" ? t.logs.connecting :
    s.status === "reconnecting" ? t.logs.reconnecting :
    s.status === "preempted" ? t.logs.takenOver :
    s.status === "error" ? t.logs.errorStream :
    s.status === "ended" ? t.logs.ended : "";
  const matchCount = s.store.filter(search, s.shown).length;

  return (
    <div>
      <div class="log-toolbar">
        <div class="field">
          <label for="log-svc">{t.logs.service}</label>
          <select id="log-svc" class="input" value={service} onChange={(e) => { s.reset(); setService((e.currentTarget as HTMLSelectElement).value); }}>
            <option value="">{t.logs.allServices}</option>
            {(svc.data?.services ?? []).map((x) => <option key={x.name} value={x.name}>{x.name}</option>)}
          </select>
        </div>
        <div class="field">
          <label for="log-tail">{t.logs.tail}</label>
          <select id="log-tail" class="input" value={String(tail)} onChange={(e) => { s.reset(); setTail(Number((e.currentTarget as HTMLSelectElement).value)); }}>
            {TAILS.map((n) => <option key={n} value={n}>{n.toLocaleString()}</option>)}
          </select>
        </div>
        <div class="field">
          <label for="log-search">{t.logs.search}</label>
          <input id="log-search" class="input" type="search" value={search} placeholder={t.logs.searchPlaceholder} onInput={(e) => setSearch((e.currentTarget as HTMLInputElement).value)} />
        </div>
        <label class="check"><input type="checkbox" checked={follow} onChange={(e) => setFollow((e.currentTarget as HTMLInputElement).checked)} /> {t.logs.follow}</label>
        <button type="button" class="btn sm" aria-pressed={s.paused} onClick={s.togglePause}>{s.paused ? t.logs.resume : t.logs.pause}</button>
        <button type="button" class="btn sm" onClick={s.clear}>{t.logs.clear}</button>
        {s.status === "preempted" ? <button type="button" class="btn sm primary" onClick={s.resume}>{t.logs.resume}</button> : null}
        <a class="btn sm" href={download} download={`${project}-${w.branch ?? w.id}.log`}><Icon name="download" /> {t.logs.download}</a>
      </div>
      {w.state === "absent" ? <Banner tone="info"><p>{t.logs.noContainers}</p></Banner> : null}
      <VirtualLog store={s.store} version={s.version} shown={s.shown} search={search} follow={follow && !s.paused} onFollowChange={setFollow} label={t.logs.viewLabel} />
      <p class="log-status">
        <span>{statusText}</span>{" · "}
        <span data-testid="log-count">{t.logs.lines(matchCount, s.shown)}</span>
        {s.paused ? <> · <span>{t.logs.paused}</span></> : null}
        {!follow ? <> · <button type="button" class="btn sm quiet" onClick={() => setFollow(true)}>{t.logs.jumpToEnd}</button></> : null}
      </p>
    </div>
  );
}
