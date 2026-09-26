package common

import (
	"bytes"
	"encoding/json"
)

func jsonCompact(buf *bytes.Buffer, raw string) error { return json.Compact(buf, []byte(raw)) }
