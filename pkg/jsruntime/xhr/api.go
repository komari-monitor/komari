package xhr

import (
	_ "embed"
)

//go:embed api.js
var xhrAPISource string
