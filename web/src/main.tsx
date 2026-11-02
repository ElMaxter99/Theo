import { render } from "preact";
import "./styles/tokens.css";
import "./styles/app.css";
import { App } from "./App.tsx";
import { applyLang } from "./lib/locale.ts";

applyLang();

const root = document.getElementById("app");
if (root) {
  root.textContent = "";
  render(<App />, root);
}
