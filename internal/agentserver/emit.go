// Ported from google.golang.org/adk/server/agentengine/internal/helper
// (Copyright 2026 Google LLC, Apache License 2.0).

package agentserver

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// flush flushes buffered data to the client.
func flush(rw http.ResponseWriter) error {
	w := rw
	for {
		switch t := w.(type) {
		case interface{ FlushError() error }:
			return t.FlushError()
		case http.Flusher:
			t.Flush()
			return nil
		case rwUnwrapper:
			w = t.Unwrap()
		default:
			return fmt.Errorf("not supported type to flush: %v %T", w, w)
		}
	}
}

// rwUnwrapper is used to get to the underlying http.ResponseWriter if
// wrapped.
type rwUnwrapper interface {
	Unwrap() http.ResponseWriter
}

// emitJSON emits a line with JSON for an event.
func emitJSON(rw http.ResponseWriter, o any) error {
	snake := convertSnake(o)
	if err := json.NewEncoder(rw).Encode(snake); err != nil {
		return fmt.Errorf("failed to encode SSE response chunk: %w", err)
	}
	if err := flush(rw); err != nil {
		return fmt.Errorf("failed to flush: %w", err)
	}
	return nil
}

// emitJSONError emits a line with json describing the error.
func emitJSONError(rw http.ResponseWriter, origError error) error {
	jsonErr := map[string]any{
		"error": origError.Error(),
	}
	if err := emitJSON(rw, jsonErr); err != nil {
		return fmt.Errorf("failed to emit error: %w", err)
	}
	return nil
}
