# Registro de cambios

Cambios destacados de cada versión, en español. Las notas de cada release en GitHub llevan esta sección después de la inglesa, con el título *Español*. Es la misma lista que [CHANGELOG.md](CHANGELOG.md), que es la de referencia: cada `## vX.Y.Z` de allí tiene aquí su sección. Las versiones anteriores a la primera de este archivo solo tienen notas en inglés.

## Unreleased

### New
- **Notificación de escritorio cuando un agente te espera o termina** (`theo config set notify.agent waiting|all`, apagada por defecto). Solo la muestra `theo-desktop`, con `notify-send`, `osascript` o PowerShell, para agentes con hooks; agrupa tres o más a la vez y nunca incluye tu prompt ni rutas. También se ajusta en Ajustes › Agentes de IA; «no avisar con la ventana en primer plano» aún no está. Probada en Linux con un sustituto; no se ha ejecutado en macOS ni Windows. Ver [agents](docs/cli/agents.md) (en inglés).
- **GitHub Copilot:** `theo agent install copilot` escribe los hooks de Theo en `.github/hooks/theo.json` (o en `~/.copilot/hooks` con `--scope user`), de modo que `theo worktrees` muestra si Copilot trabaja, espera o ha terminado, y la sesión sabe dónde se ejecuta. `theo agent install claude` equivale a `theo mcp install --hooks`. Theo usa los agentes que ya tienes y nunca los instala: `theo agent install` sin nombre registra los que encuentra y dice dónde conseguir los demás, y con nombre rechaza un agente ausente salvo que pases `--force`. Escrito a partir de la documentación de GitHub y probado solo con ejemplos; aún no se ha ejecutado con Copilot. Ver [agents](docs/cli/agents.md) (en inglés).

### Changed
- **La interfaz web se compila con preact 11 y TypeScript 7** (dependencias de desarrollo de `web/`; la interfaz se ve y funciona igual). El campo de contraseña del formulario de variables son ahora dos `input` literales, que es lo que exigen los tipos de preact 11.
- **Theo se compila con Go 1.26** (hace falta Go 1.26 o posterior para compilarlo desde el código fuente). Go 1.24 ya no recibe correcciones de seguridad y `govulncheck` encontró 25 vulnerabilidades en la biblioteca estándar con la que se compilaba; los binarios de las próximas versiones llevan las correcciones.
