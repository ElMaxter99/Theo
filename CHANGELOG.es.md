# Registro de cambios

Cambios destacados de cada versión, en español. Las notas de cada release en GitHub llevan esta sección después de la inglesa, con el título *Español*. Es la misma lista que [CHANGELOG.md](CHANGELOG.md), que es la de referencia: cada `## vX.Y.Z` de allí tiene aquí su sección. Las versiones anteriores a la primera de este archivo solo tienen notas en inglés.

## Unreleased

### Changed
- **La interfaz web se compila con preact 11 y TypeScript 7** (dependencias de desarrollo de `web/`; la interfaz se ve y funciona igual). El campo de contraseña del formulario de variables son ahora dos `input` literales, que es lo que exigen los tipos de preact 11.
- **Theo se compila con Go 1.26** (hace falta Go 1.26 o posterior para compilarlo desde el código fuente). Go 1.24 ya no recibe correcciones de seguridad y `govulncheck` encontró 25 vulnerabilidades en la biblioteca estándar con la que se compilaba; los binarios de las próximas versiones llevan las correcciones.
