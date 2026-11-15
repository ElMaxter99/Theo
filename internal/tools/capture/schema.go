package capture

import _ "embed"

// Schema is the JSON Schema of pantallas.json (version 1). It is also what an
// AI is given, so what it writes is valid by construction.
//
//go:embed schema/pantallas.v1.json
var Schema []byte
