// Language selector (header and settings). The names of the languages are written in their own language and marked
// with `lang`, so a screen reader pronounces them correctly whatever the active language is.
import { useState } from "preact/hooks";
import { readLangPref, saveLangPref } from "../lib/locale.ts";
import type { LangPref } from "../lib/locale.ts";
import { t } from "../strings.ts";

export function LanguageSelect({ id, describedBy }: { id: string; describedBy?: string }) {
  const [pref, setPref] = useState<LangPref>(readLangPref());
  const change = (v: string) => {
    const p: LangPref = v === "en" || v === "es" ? v : "system";
    setPref(p);
    saveLangPref(p); // reloads the page when the active language changes
  };
  return (
    <select id={id} class="input auto" value={pref} aria-describedby={describedBy} onChange={(e) => change((e.currentTarget as HTMLSelectElement).value)}>
      <option value="system">{t.app.langSystem}</option>
      <option value="en" lang="en">{t.app.langEn}</option>
      <option value="es" lang="es">{t.app.langEs}</option>
    </select>
  );
}
