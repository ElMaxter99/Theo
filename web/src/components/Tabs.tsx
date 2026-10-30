// ARIA tabs pattern with automatic activation: Left/Right (and Home/End) move and select, Tab goes to the panel.
import { useRef } from "preact/hooks";
import type { ComponentChildren } from "preact";

export interface TabDef {
  id: string;
  label: string;
}

export function Tabs({ tabs, selected, onSelect, label, idBase, children }: {
  tabs: TabDef[]; selected: string; onSelect: (id: string) => void; label: string; idBase: string; children: ComponentChildren;
}) {
  const refs = useRef<Record<string, HTMLButtonElement | null>>({});
  const onKey = (e: KeyboardEvent, i: number) => {
    let next = -1;
    if (e.key === "ArrowRight") next = (i + 1) % tabs.length;
    else if (e.key === "ArrowLeft") next = (i - 1 + tabs.length) % tabs.length;
    else if (e.key === "Home") next = 0;
    else if (e.key === "End") next = tabs.length - 1;
    if (next < 0) return;
    e.preventDefault();
    const id = tabs[next]!.id;
    onSelect(id);
    refs.current[id]?.focus();
  };
  return (
    <>
      <div class="tablist" role="tablist" aria-label={label}>
        {tabs.map((tab, i) => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`${idBase}-tab-${tab.id}`}
            aria-selected={tab.id === selected}
            aria-controls={`${idBase}-panel`}
            tabIndex={tab.id === selected ? 0 : -1}
            ref={(el) => { refs.current[tab.id] = el; }}
            onClick={() => onSelect(tab.id)}
            onKeyDown={(e) => onKey(e, i)}
          >
            {tab.label}
          </button>
        ))}
      </div>
      <div role="tabpanel" id={`${idBase}-panel`} aria-labelledby={`${idBase}-tab-${selected}`} tabIndex={0}>
        {children}
      </div>
    </>
  );
}
