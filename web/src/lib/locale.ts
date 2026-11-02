// Interface language (Q-029): English is the default, Spanish is available. The preference is "system" (follow the
// browser: es* -> Spanish, anything else -> English), "en" or "es"; it is stored in localStorage like the theme, always
// inside try/catch. The language is resolved once when the page loads: changing it saves the choice and reloads the
// page, so every module can keep importing a plain `t` (see strings.ts) instead of subscribing to a language signal.
export type Lang = "en" | "es";
export type LangPref = "system" | Lang;
const KEY = "theo.lang";

export function readLangPref(): LangPref {
  try {
    const v = localStorage.getItem(KEY);
    if (v === "en" || v === "es") return v;
  } catch { /* storage unavailable: follow the browser */ }
  return "system";
}

/** The browser's preferred language, or "" outside a browser (unit tests stay in English whatever the machine's locale). */
export function browserLanguage(): string {
  if (typeof document === "undefined" || typeof navigator === "undefined") return "";
  return navigator.language || "";
}

export function resolveLang(pref: LangPref, browser: string): Lang {
  if (pref !== "system") return pref;
  return /^es(-|$)/i.test(browser) ? "es" : "en";
}

/** BCP 47 tag for Intl: the browser's own tag when it is a variant of the active language (es-MX, en-GB), else a default. */
export function intlLocaleFor(lang: Lang, browser: string): string {
  if (browser && browser.toLowerCase().split("-")[0] === lang) return browser;
  return lang === "es" ? "es-ES" : "en-US";
}

export const lang: Lang = resolveLang(readLangPref(), browserLanguage());
export const intlLocale: string = intlLocaleFor(lang, browserLanguage());

/** Sets <html lang> to the active language (called before the first render). */
export function applyLang(): void {
  if (typeof document !== "undefined") document.documentElement.lang = lang;
}

/**
 * Saves the choice and reloads the page when it changes the active language. Returns whether a reload was started.
 * Without working storage the choice cannot survive the reload, so nothing is reloaded and the language stays as it is.
 */
export function saveLangPref(pref: LangPref): boolean {
  try {
    if (pref === "system") localStorage.removeItem(KEY);
    else localStorage.setItem(KEY, pref);
  } catch { /* the choice just does not persist */ }
  if (readLangPref() !== pref) return false;
  if (resolveLang(pref, browserLanguage()) === lang) return false;
  location.reload();
  return true;
}

let nf: Intl.NumberFormat | undefined;
/** A number formatted for the active locale (1,234 in English, 1234 / 12.345 in Spanish). */
export function num(n: number): string {
  nf ??= new Intl.NumberFormat(intlLocale);
  return nf.format(n);
}

let pr: Intl.PluralRules | undefined;
/** "one" or "other" for the active locale (Spanish "many" is folded into "other"). */
export function pluralCategory(n: number): "one" | "other" {
  pr ??= new Intl.PluralRules(intlLocale);
  return pr.select(n) === "one" ? "one" : "other";
}

/** "3 worktrees": the count formatted for the locale and the singular or plural noun (English default: noun + "s"). */
export const plural = (n: number, one: string, many: string = one + "s"): string => `${num(n)} ${pluralCategory(n) === "one" ? one : many}`;
