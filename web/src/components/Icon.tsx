// One coherent set of functional icons (16x16, currentColor). Never mascot drawings (APP_UI_GUIDELINES 6.4).
// Every icon is decorative: the text next to it carries the meaning, so icons are aria-hidden.
import type { JSX } from "preact";

export type IconName =
  | "running" | "partial" | "unhealthy" | "stopped" | "absent" | "unknown" | "portbusy" | "busy"
  | "ok" | "error" | "info" | "warn" | "folder" | "git" | "play" | "stop" | "restart" | "back" | "external"
  | "ops" | "plus" | "trash" | "eye" | "search" | "download" | "lock" | "dot" | "sun" | "moon" | "arrow";

const shapes: Record<IconName, JSX.Element> = {
  // Circle with a play triangle.
  running: <><circle cx="8" cy="8" r="7" fill="currentColor" /><path d="M6.2 4.8v6.4l5-3.2z" class="cut" /></>,
  // Half-filled circle.
  partial: <><circle cx="8" cy="8" r="6.2" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="M8 1.8a6.2 6.2 0 0 1 0 12.4z" fill="currentColor" /></>,
  // Warning triangle with an exclamation mark.
  unhealthy: <><path d="M8 1.2 15.2 14H.8z" fill="currentColor" /><path d="M8 5.6v4M8 11.3v.1" class="cut-s" strokeWidth="1.6" strokeLinecap="round" /></>,
  // Square (stop symbol).
  stopped: <rect x="2.2" y="2.2" width="11.6" height="11.6" rx="1.6" fill="currentColor" />,
  // Empty circle.
  absent: <circle cx="8" cy="8" r="6.2" fill="none" stroke="currentColor" strokeWidth="1.6" strokeDasharray="2.2 1.6" />,
  // Circle with question mark.
  unknown: <><circle cx="8" cy="8" r="6.2" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="M6.1 6.2a1.9 1.9 0 1 1 2.7 1.7c-.6.3-.8.7-.8 1.3M8 11.4v.1" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /></>,
  // Octagon with exclamation mark.
  portbusy: <><path d="M5.2 1h5.6L15 5.2v5.6L10.8 15H5.2L1 10.8V5.2z" fill="currentColor" /><path d="M8 4.6v4.2M8 10.9v.1" class="cut-s" strokeWidth="1.6" strokeLinecap="round" /></>,
  // Spinner arc (static when motion is reduced, text carries the meaning).
  busy: <><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" strokeWidth="1.8" opacity=".3" /><path d="M8 2a6 6 0 0 1 6 6" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></>,
  ok: <><circle cx="8" cy="8" r="7" fill="currentColor" /><path d="m4.6 8.2 2.3 2.3 4.5-4.7" fill="none" class="cut-s" strokeWidth="1.7" strokeLinecap="round" strokeLinejoin="round" /></>,
  error: <><circle cx="8" cy="8" r="7" fill="currentColor" /><path d="m5.4 5.4 5.2 5.2M10.6 5.4l-5.2 5.2" class="cut-s" strokeWidth="1.7" strokeLinecap="round" /></>,
  info: <><circle cx="8" cy="8" r="7" fill="currentColor" /><path d="M8 7.2v4M8 4.9v.1" class="cut-s" strokeWidth="1.7" strokeLinecap="round" /></>,
  warn: <><path d="M8 1.2 15.2 14H.8z" fill="currentColor" /><path d="M8 5.6v4M8 11.3v.1" class="cut-s" strokeWidth="1.6" strokeLinecap="round" /></>,
  folder: <path d="M1.5 3.5h4.2l1.4 1.6h7.4v7.9a1 1 0 0 1-1 1H2.5a1 1 0 0 1-1-1z" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinejoin="round" />,
  git: <><circle cx="4" cy="3.5" r="1.7" fill="none" stroke="currentColor" strokeWidth="1.4" /><circle cx="4" cy="12.5" r="1.7" fill="none" stroke="currentColor" strokeWidth="1.4" /><circle cx="12" cy="6.5" r="1.7" fill="none" stroke="currentColor" strokeWidth="1.4" /><path d="M4 5.2v5.6M12 8.2c0 2.2-3 1.8-7 3.2" fill="none" stroke="currentColor" strokeWidth="1.4" /></>,
  play: <path d="M4.5 2.6v10.8l9-5.4z" fill="currentColor" />,
  stop: <rect x="3" y="3" width="10" height="10" rx="1.4" fill="currentColor" />,
  restart: <path d="M13.2 8A5.2 5.2 0 1 1 11.6 4.3M13.4 1.8v3.2h-3.2" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />,
  back: <path d="M10 2.5 4.5 8l5.5 5.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />,
  arrow: <path d="M6 2.5 11.5 8 6 13.5" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />,
  external: <path d="M9 2.5h4.5V7M13.5 2.5 7.5 8.5M11.5 9.5v3a1 1 0 0 1-1 1h-7a1 1 0 0 1-1-1v-7a1 1 0 0 1 1-1h3" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />,
  ops: <path d="M2 4h12M2 8h12M2 12h8" fill="none" stroke="currentColor" strokeWidth="1.7" strokeLinecap="round" />,
  plus: <path d="M8 2.5v11M2.5 8h11" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" />,
  trash: <path d="M2.5 4.5h11M6 4.5V3h4v1.5M4 4.5l.6 8.5h6.8l.6-8.5M6.6 7v4M9.4 7v4" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" strokeLinejoin="round" />,
  eye: <><path d="M1 8s2.5-4.5 7-4.5S15 8 15 8s-2.5 4.5-7 4.5S1 8 1 8z" fill="none" stroke="currentColor" strokeWidth="1.5" /><circle cx="8" cy="8" r="2" fill="currentColor" /></>,
  search: <><circle cx="7" cy="7" r="4.5" fill="none" stroke="currentColor" strokeWidth="1.6" /><path d="m10.5 10.5 3.5 3.5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" /></>,
  download: <path d="M8 2v8M4.5 7 8 10.5 11.5 7M2.5 13.5h11" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round" />,
  lock: <><rect x="3" y="7" width="10" height="7" rx="1.4" fill="currentColor" /><path d="M5.2 7V5a2.8 2.8 0 0 1 5.6 0v2" fill="none" stroke="currentColor" strokeWidth="1.5" /></>,
  dot: <circle cx="8" cy="8" r="2.5" fill="currentColor" />,
  sun: <><circle cx="8" cy="8" r="3" fill="currentColor" /><path d="M8 1v2M8 13v2M1 8h2M13 8h2M3 3l1.4 1.4M11.6 11.6 13 13M3 13l1.4-1.4M11.6 4.4 13 3" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" /></>,
  moon: <path d="M13.5 9.5A5.5 5.5 0 0 1 6.5 2.5a5.5 5.5 0 1 0 7 7z" fill="currentColor" />,
};

export function Icon({ name, size = 16, spin = false }: { name: IconName; size?: number; spin?: boolean }) {
  return (
    <svg class={spin ? "spin" : undefined} width={size} height={size} viewBox="0 0 16 16" aria-hidden="true" focusable="false">
      {shapes[name]}
    </svg>
  );
}
