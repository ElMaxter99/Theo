// Theo's symbol (the branch with three nodes, brand/symbol.svg) as the loading indicator: the branches draw
// themselves and the nodes pop in one after another. Colours come from tokens.css; with reduced motion the
// symbol is simply shown complete (see .mark in app.css).
export function Mark({ size = 24, class: cls }: { size?: number; class?: string }) {
  return (
    <svg class={`mark${cls ? ` ${cls}` : ""}`} width={size} height={size} viewBox="0 0 256 256" aria-hidden="true" focusable="false">
      <path class="mark-branch b1" pathLength={1} d="M84.0,66.0 C84.0,74.7 80.7,104.0 84.0,118.0 C87.3,132.0 90.0,136.7 104.0,150.0 C118.0,163.3 157.3,190.0 168.0,198.0" />
      <path class="mark-branch b2" pathLength={1} d="M98.0,140.0 C103.7,137.3 119.7,127.7 132.0,124.0 C144.3,120.3 165.3,119.0 172.0,118.0" />
      <circle class="mark-node n1" cx="84" cy="66" r="22" />
      <circle class="mark-node n2" cx="172" cy="118" r="22" />
      <circle class="mark-node n3" cx="168" cy="198" r="22" />
    </svg>
  );
}
