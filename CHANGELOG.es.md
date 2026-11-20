# Registro de cambios

Cambios destacados de cada versión, en español. Las notas de cada release en GitHub llevan esta sección después de la inglesa, con el título *Español*. Es la misma lista que [CHANGELOG.md](CHANGELOG.md), que es la de referencia: cada `## vX.Y.Z` de allí tiene aquí su sección. Las versiones anteriores a la primera de este archivo solo tienen notas en inglés.

## v0.11.0

Theo ya se instala como cualquier otro programa y se actualiza solo desde la app abierta. Hay un instalador por sistema, no pide permisos de administrador y las versiones nuevas (y, si lo pides, las de prueba) se ofrecen con un aviso que aceptas tú.

### Nuevo
- **Instaladores.** Windows: `theo-setup_vX.Y.Z_windows_amd64.exe` (por usuario, menú Inicio, `theo` en tu PATH, desinstalador en Aplicaciones). macOS: `Theo_vX.Y.Z_darwin_arm64.dmg` o `_amd64.dmg` (arrastra Theo.app a Aplicaciones; la primera vez, clic derecho > Abrir). Linux: `theo-installer_vX.Y.Z_linux_amd64.sh` (también arm64; por usuario, entrada en el menú, `theo-uninstall`) y `theo_vX.Y.Z_amd64.deb` para Debian y Ubuntu.
- **`theo update`.** `theo update --check` dice si hay una versión más nueva; `theo update` la descarga, comprueba su SHA-256 con `checksums.txt` y sustituye Theo (el paquete entero en macOS), con vuelta atrás si el cambio falla. Sin archivo para tu sistema o sin sumas no instala nada y enseña la página de la versión.
- **Actualizaciones en la app.** La app de escritorio comprueba al arrancar y cada seis horas, muestra un aviso y solo instala cuando pulsas el botón; después se reinicia. Ajustes > Actualizaciones tiene la comprobación, el canal y el interruptor del aviso; la interfaz web y `/api/v1/update*` ofrecen lo mismo.
- **Canal de versiones de prueba.** `update-channel` es `stable` (por defecto) o `rc`; las versiones de prueba se comparan por la versión completa, así que una prueba más nueva nunca sustituye a la estable más reciente. `update-check` viene activado por defecto solo en la app de escritorio.
- Sin probar en equipos reales de Windows y macOS: el asistente y su cambio de PATH, los avisos nativos, el reinicio tras actualizar, Gatekeeper con el `.dmg` y la instalación del `.deb` con apt.

## v0.10.0

La herramienta de capturas ya puede escribir su propio inventario de pantallas: una IA lee el proyecto y redacta `pantallas.json` y sus mocks, empareja las pantallas que las reglas no lograron casar entre dos ramas y propone cómo arrancar un proyecto. Solo lee y tú decides qué se guarda.

### Nuevo
- **Generar el inventario de pantallas con IA (0.10).** `theo tool ai doctor` encuentra Claude Code y GitHub Copilot, los prueba y muestra el proveedor y el modelo de cada rol (`ai.roles.coordinator`, `ai.roles.worker`). `theo tool capture generate` explora el proyecto y escribe `pantallas.json` y sus mocks con trabajadores en paralelo; cada respuesta se valida y se devuelve si está mal, una ejecución se guarda y se reanuda con `--resume`, y lo que no se pudo generar se lista. La IA solo lee: el resultado es un borrador y `theo tool capture accept` lo escribe, sin sobrescribir nunca un `pantallas.json` existente salvo que elijas `--replace` (o `--merge`, que solo añade las pantallas que faltan). La primera vez en un proyecto Theo avisa de que el código se envía al proveedor y pregunta. `--verify` captura el borrador con la aplicación en marcha y devuelve los fallos a la IA (dos vueltas).
- **Las pantallas que las reglas no logran emparejar se emparejan con IA.** `run-pair` y `compare` consultan al coordinador sobre las pantallas que solo tiene una rama (títulos, rutas y nombres de archivo, nunca código). Los pares se aplican con su confianza, se marcan «por IA» en los informes y se guardan en `.theo/capturas/emparejamientos.json`, donde un par escrito a mano manda siempre. `--no-ai` lo omite.
- **`theo tool capture detect --ai`** propone una sección `run` cuando las reglas no encuentran nada, validada como una escrita a mano.
- Sin probar con los CLI reales de Claude Code y Copilot: los adaptadores siguen su documentación y se prueban con un proveedor de pruebas.

## v0.9.0

La herramienta de capturas ya hace capturas: compara dos ramas de un proyecto pantalla a pantalla y escribe los informes, desde la línea de comandos y desde una nueva vista Herramientas.

### Nuevo
- **Capturas antes/después (0.9).** `theo tool capture` ahora captura, compara e informa. `.theo/capturas/pantallas.json` lista las pantallas, estados y flujos (`validate`, `init`); `setup` instala el motor (Node, Playwright, Chromium) una vez; `run` captura un despliegue dos veces para encontrar lo inestable, con el reloj congelado, sin animaciones y con datos simulados; `run-pair` levanta dos ramas, captura, compara y escribe los informes; `compare`, `sets` y `report` trabajan con conjuntos guardados. La comparación empareja pantallas por id, ruta o archivo de origen compartido, clasifica cada una como igual, cambio menor, cambiada, nueva, eliminada, movida, inestable o no capturada, compara los flujos y lista lo que no pudo cubrir. Tres informes: corporativo (escritorio y móvil lado a lado, HTML y PDF), composiciones antes/después con la diferencia marcada y un resumen en Markdown con el porcentaje de píxeles cambiados. Se escriben en el idioma de Theo (nueva preferencia `language`: `en`, `es` o `system`).
- **Vista «Herramientas» en la interfaz web.** Elige dos ramas, empieza, sigue el progreso, abre los informes, instala el motor y mira los conjuntos guardados. Usa operaciones, así que sigue aunque cierres la página.
- **`theo doctor` comprueba el motor de capturas** en los proyectos que tienen inventario.

## v0.8.0

La primera herramienta de Theo: `capture` averigua cómo se arranca un proyecto y levanta varias ramas a la vez, incluso una aplicación AngularJS que solo genera ficheros y tiene que vivir bajo `/portal`. Es la base de las capturas antes/después de las próximas versiones.

### Nuevo
- **Marco de herramientas (0.8).** `theo tool list` muestra las herramientas de Theo. La primera, `capture` (de momento sin capturas de pantalla: llegan en la siguiente versión), tiene `detect` (propone cómo arranca un proyecto: Compose, un script de `package.json` o un objetivo de Makefile) y `serve` (arranca lo que describe la sección `run` de `.theo.json`). La sección `run` se aprueba como `after_create`.
- **Tipo `static` en la sección `run`.** Para una aplicación que solo genera una carpeta (AngularJS bajo `/portal`): `build`, `dir`, `base`, `spa` y `proxy`. `serve` instala, compila y sirve la carpeta él mismo bajo `base`, en un puerto libre por worktree, así que dos ramas responden en `/portal/` a la vez. Avisa si la compilación deja un `<base href>` absoluto y, cuando falla una instalación o una compilación, muestra las últimas líneas que imprimió. `detect` reconoce AngularJS 1.x y propone la sección.

## v0.7.3

Versión de corrección: una opción para no avisar mientras la ventana de Theo está delante, y pruebas más estables.

### Nuevo
- **No avisar con la ventana al frente.** La app de escritorio sabe cuándo su ventana está delante y, si lo pides, no muestra entonces el aviso de un agente (Ajustes > Agentes de IA, o `theo config set notify.skip-focused on`; apagado por defecto, así que nada cambia si no lo activas). Lo que cambia mientras miras no se anuncia después. `POST /notify` acepta `skip_focused` junto a `mode`. Probado con un sustituto; sin ejecutar aún en macOS ni Windows.

### Corregido
- **`TestServicesRecreateAndExec` ya no falla de vez en cuando.** Docker podía listar un contenedor como en marcha justo después de `compose stop`; la prueba ahora espera al estado real. Sin cambios en Theo.

### Cambiado
- **Las pruebas e2e contra el servidor real funcionan en Windows** (`theo.exe`, `netstat`; Playwright pasa a ser dependencia de desarrollo de `web/`) y el CI las ejecuta. Se regeneran las capturas de `web/e2e/screenshots`. Sin cambios en Theo.

## v0.7.2

Versión de corrección: interfaz clara por defecto, y capturas y README que la muestran.

### Cambiado
- **Paleta clara como los lienzos de diseño.** La interfaz abre por defecto en el tema claro, aunque el sistema esté en oscuro (en Ajustes > Tema siguen Sistema y Oscuro, y se recuerda la elección). La página es crema en todo el ancho con tarjetas blancas, y el tema oscuro es carbón cálido en vez de ciruela. Todos los pares de texto siguen cumpliendo WCAG AA.
- **Las capturas del README y la documentación de la interfaz enseñan la paleta nueva.** Se regeneran las imágenes de `README.md`, `README.es.md` y `web/e2e/screenshots`, y `docs/cli/ui-web.md` describe el tema claro por defecto y el oscuro carbón cálido.

## v0.7.1

Versión de corrección: la limpieza se rediseña como un flujo de cuatro pasos y se actualizan las dependencias de compilación.

### Changed
- **La limpieza es un flujo de cuatro pasos con un estado por worktree.** Problemas > Limpieza pasa ahora por Revisar, Confirmar, Quitando y Resultado. Cada worktree está Listo, Espera tu OK, Bloqueado o Conservado, y cuatro contadores suman el total y filtran la lista. Se agrupan por lo que decides (por ejemplo «carpetas que Git ignora y no se pueden regenerar»: una decisión para todos, con `certs/` o `.claude/` por su nombre), las columnas dicen qué se guarda solo y qué se borra para siempre, «Ver archivos» abre la fila y cada fila bloqueada dice cómo desbloquearla. La confirmación lista lo que se quita, lo que se guarda antes, lo que se borra para siempre y lo que se conserva; el resultado cuenta qué pasó con cada worktree, con «Reintentar» y un enlace para retomarlo. Los fusionados con archivos sin confirmar también se pueden quitar (antes se guardan en un commit local). `POST /projects/{p}/clean` acepta `paths` para cerrar solo algunos, y `POST /projects/{p}/park-inactive` acepta un worktree fusionado nombrado en `paths`. Quitar la carpeta pasa siempre por aparcar (la rama se queda y se puede retomar); la opción de borrar imágenes de la página web queda solo en `theo worktree clean --images`.
- **Dependencia de la interfaz:** preact 11.2.0 (antes 11.1.1); la interfaz se ve y funciona igual.
- **Los flujos de publicación y CI usan versiones nuevas de GitHub Actions** (`actions/checkout` 7, `actions/setup-go` 7, `actions/download-artifact` 8). Sin cambios en Theo ni en sus binarios.

## v0.7.0

Claude Code y GitHub Copilot en el flujo de los agentes, y un aviso de escritorio cuando un agente te necesita.

### New
- **Notificación de escritorio cuando un agente te espera o termina** (`theo config set notify.agent waiting|all`, apagada por defecto). Solo la muestra `theo-desktop`, con `notify-send`, `osascript` o PowerShell, para agentes con hooks; agrupa tres o más a la vez y nunca incluye tu prompt ni rutas. También se ajusta en Ajustes › Agentes de IA; «no avisar con la ventana en primer plano» aún no está. Probada en Linux con un sustituto; no se ha ejecutado en macOS ni Windows. Ver [agents](docs/cli/agents.md) (en inglés).
- **GitHub Copilot:** `theo agent install copilot` escribe los hooks de Theo en `.github/hooks/theo.json` (o en `~/.copilot/hooks` con `--scope user`), de modo que `theo worktrees` muestra si Copilot trabaja, espera o ha terminado, y la sesión sabe dónde se ejecuta. `theo agent install claude` equivale a `theo mcp install --hooks`. Theo usa los agentes que ya tienes y nunca los instala: `theo agent install` sin nombre registra los que encuentra y dice dónde conseguir los demás, y con nombre rechaza un agente ausente salvo que pases `--force`. Escrito a partir de la documentación de GitHub y probado solo con ejemplos; aún no se ha ejecutado con Copilot. Ver [agents](docs/cli/agents.md) (en inglés).
- **Quitar en bloque los worktrees inactivos muestra su progreso.** La ventana de Limpieza ya no deja el botón desactivado hasta el final: cierra la confirmación y abre una ventana con «N de M», el worktree que se está tratando, el resultado de cada uno (quitado o conservado, con el motivo) y un resultado final. El trabajo se ejecuta en el servidor como una operación (`POST /projects/{p}/park-inactive` ahora responde `202`; consulta `GET /projects/{p}/park-inactive/{op}`), de modo que cerrar la ventana no lo detiene. `theo worktree park-inactive` no cambia.

### Fixed
- **Limpieza, worktrees inactivos:** la interfaz pedía el plan con `?save_changes=1` y el servidor solo leía `true`, así que las opciones no tenían efecto y todo worktree con cambios seguía sin poder elegirse. El servidor lee ambos. Las carpetas ignoradas que se pueden reconstruir (`node_modules`, `logs`, `dist`...) ya no impiden aparcar, un HEAD desacoplado se puede conservar en una rama `theo-rescue/<carpeta>` (`--rescue-detached`), los motivos se muestran en el idioma de la interfaz, y guardar cambios y rescatar vienen activados en la interfaz.

### Changed
- **La interfaz web se compila con preact 11 y TypeScript 7** (dependencias de desarrollo de `web/`; la interfaz se ve y funciona igual). El campo de contraseña del formulario de variables son ahora dos `input` literales, que es lo que exigen los tipos de preact 11.
- **Theo se compila con Go 1.26** (hace falta Go 1.26 o posterior para compilarlo desde el código fuente). Go 1.24 ya no recibe correcciones de seguridad y `govulncheck` encontró 25 vulnerabilidades en la biblioteca estándar con la que se compilaba; los binarios de las próximas versiones llevan las correcciones.
