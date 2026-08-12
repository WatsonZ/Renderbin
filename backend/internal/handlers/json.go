package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
)

// maxJSONBody caps the request body of every endpoint that takes a small JSON
// payload -- credentials, settings, a name, a tag string, an expiry spec.
//
// The cap is not optional. encoding/json buffers the whole top-level value
// before it can decode it, so an uncapped Decode makes the request body the
// heap: one unauthenticated POST /api/auth/login with a 64MB body took this
// server from 21MB to 423MB of RSS, and a handful in parallel is enough to OOM
// the 512MB VPS this app is meant to be self-hosted on. 64KB is far more than
// any of these payloads legitimately needs.
//
// The two file endpoints that carry a document use maxFileBody instead; the
// MCP endpoint sets its own, larger cap in mcp.go.
const maxJSONBody = 64 << 10

// decodeJSON reads a capped, JSON-encoded body into dst. It writes the error
// response itself and reports whether the caller should continue.
//
// An oversized body is 413 with a distinct message rather than 400 "invalid
// request body": MaxBytesReader surfaces as a decode failure, so folding the
// two together tells someone whose upload was too big that their JSON was
// malformed -- which is what the file endpoints used to do, and it sent people
// looking for a syntax error that was never there.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any, limit int64) bool {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return false
		}
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return false
	}
	return true
}

// decodeSmallJSON is decodeJSON at the default maxJSONBody cap -- the right
// call for everything except the two endpoints that carry a document.
func decodeSmallJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	return decodeJSON(w, r, dst, maxJSONBody)
}

// writeJSON encodes v as a 200 response body.
func writeJSON(w http.ResponseWriter, v any) {
	writeJSONStatus(w, http.StatusOK, v)
}

// writeJSONStatus encodes v under an explicit status code.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
