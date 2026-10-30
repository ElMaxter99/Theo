// Small presentational building blocks.
import type { ComponentChildren } from "preact";
import { Icon } from "./Icon.tsx";
import type { IconName } from "./Icon.tsx";
import type { StatusView, Tone } from "../lib/status.ts";
import { ApiError } from "../api/client.ts";
import { t } from "../strings.ts";

export function Badge({ view, extra }: { view: StatusView; extra?: string }) {
  return (
    <span class={`badge ${view.tone}`}>
      <Icon name={view.icon} spin={view.spin} />
      <span>{view.label}</span>
      {extra ? <span class="sr-only">{extra}</span> : null}
    </span>
  );
}

const bannerIcon: Record<Tone, IconName> = { ok: "ok", warn: "warn", error: "error", info: "info", neutral: "info" };

export function Banner({ tone, title, children, actions, role }: { tone: Tone; title?: string; children?: ComponentChildren; actions?: ComponentChildren; role?: "alert" | "status" }) {
  return (
    <div class={`banner ${tone}`} role={role}>
      <Icon name={bannerIcon[tone]} size={20} />
      <div class="banner-body">
        {title ? <p><strong>{title}</strong></p> : null}
        {children}
      </div>
      {actions}
    </div>
  );
}

/** Shows an API error with its message, hint and (collapsed) original tool output. */
export function ErrorBanner({ error, title }: { error: ApiError | { message: string } | undefined; title?: string }) {
  if (!error) return null;
  const body = error instanceof ApiError ? error.body : undefined;
  return (
    <Banner tone="error" title={title ?? t.banner.error} role="alert">
      <p>{error.message}</p>
      {body?.hint ? <p>{body.hint}</p> : null}
      {body?.original ? (
        <details>
          <summary>{t.banner.detailsOriginal}</summary>
          <pre class="mono small prewrap">{body.original}</pre>
        </details>
      ) : null}
    </Banner>
  );
}

export function Loading({ label = t.app.loading }: { label?: string }) {
  return (
    <p class="muted" role="status">
      <Icon name="busy" spin /> {label}
    </p>
  );
}

export function PageHead({ title, sub, actions, headingRef }: { title: ComponentChildren; sub?: ComponentChildren; actions?: ComponentChildren; headingRef?: preact.Ref<HTMLHeadingElement> }) {
  return (
    <div class="page-head">
      <div>
        <h1 ref={headingRef} tabIndex={-1}>{title}</h1>
        {sub ? <p class="sub">{sub}</p> : null}
      </div>
      {actions ? <div class="row">{actions}</div> : null}
    </div>
  );
}

export function Crumbs({ items }: { items: { label: string; href?: string }[] }) {
  return (
    <nav aria-label="Breadcrumb">
      <ol class="crumbs">
        {items.map((it, i) => (
          <li key={i}>
            {it.href ? <a href={it.href}>{it.label}</a> : <span aria-current="page">{it.label}</span>}
          </li>
        ))}
      </ol>
    </nav>
  );
}

export function Field({ id, label, hint, error, children }: { id: string; label: string; hint?: string; error?: string; children: ComponentChildren }) {
  return (
    <div class="field">
      <label for={id}>{label}</label>
      {children}
      {hint ? <p class="hint" id={`${id}-hint`}>{hint}</p> : null}
      {error ? <p class="field-error" id={`${id}-err`}><Icon name="error" /> <span>{error}</span></p> : null}
    </div>
  );
}

export function describedBy(id: string, hint?: string, error?: string): { "aria-describedby": string | undefined; "aria-invalid": "true" | undefined } {
  const ids = [hint ? `${id}-hint` : "", error ? `${id}-err` : ""].filter(Boolean).join(" ");
  return { "aria-describedby": ids || undefined, "aria-invalid": error ? "true" : undefined };
}

/** Real link that opens in a new tab with the protections from the plan. */
export function ExternalLink({ href, children, label }: { href: string; children: ComponentChildren; label?: string }) {
  // Only http(s) links are ever rendered as links (project data is untrusted).
  const safe = /^https?:\/\//i.test(href) ? href : undefined;
  if (!safe) return <span>{children}</span>;
  return (
    <a href={safe} target="_blank" rel="noopener noreferrer">
      {children} <Icon name="external" size={13} />
      <span class="sr-only"> {label ? `${label} ` : ""}{t.ports.newTab}</span>
    </a>
  );
}
