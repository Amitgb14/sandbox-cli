package studioapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends the failure to the caller and, for a server-side one, to the
// log as well.
//
// Only 5xx is logged, and the split is the useful one. A 4xx is the caller's
// mistake and it already has the answer in the response — logging every rejected
// form would bury the lines that matter. A 5xx is *this* server failing to do
// something it accepted, and the operator reading `docker compose logs api` is
// often not the person who saw the response: a browser showing "502" with
// nothing in the log means the only way to learn the reason is to reproduce the
// request, which is exactly the position this was in when a launch failed on a
// brokered secret the container could not resolve.
func writeError(w http.ResponseWriter, status int, err error) {
	if status >= 500 {
		log.Printf("sandbox-studio-api: %d %v", status, err)
	}
	writeJSON(w, status, ErrorResponse{Error: err.Error()})
}

// decodeJSON reads the request body into v. A missing or empty body decodes to
// v's zero value rather than erroring, since every request body in this API is
// optional (every field of every *Request type has a usable default).
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return nil
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return fmt.Errorf("invalid JSON body: %w", err)
	}
	return nil
}
