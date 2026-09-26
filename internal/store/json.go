package store

import "encoding/json"

// jsonMarshal wraps encoding/json's Marshal so callers within the store
// package can produce JSON without importing the encoding package directly.
func jsonMarshal(value any) ([]byte, error) {
	return json.Marshal(value)
}
