// Package codegen generates the plain-text storage/item codes (architecture
// plan §4). Shared between internal/api (REST create endpoints) and
// internal/mcpserver (the add_item/add_storage MCP tools) so both create
// paths produce identically-shaped codes without duplicating the alphabet.
package codegen

import "crypto/rand"

// PlainTextCodeAlphabet is Crockford's Base32 alphabet minus I, L, O, U —
// chosen (architecture plan §4) to avoid characters that are visually
// ambiguous on a hand-written sticky note or a plain text-only label.
// Mirrors web/src/lib/api.ts's generatePlainTextCode() exactly.
const PlainTextCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// PlainTextCode generates a new 6-character code from PlainTextCodeAlphabet,
// drawn from crypto/rand so codes aren't predictable if they're ever used as
// capability tokens. The alphabet is exactly 32 characters, so masking a
// random byte to its low 5 bits picks uniformly with no modulo bias.
func PlainTextCode() string {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		// crypto/rand.Read never returns an error on supported platforms
		// (Go 1.24+ crashes the process itself instead); mirror that here.
		panic("codegen: crypto/rand unavailable: " + err.Error())
	}
	b := make([]byte, len(raw))
	for i, r := range raw {
		b[i] = PlainTextCodeAlphabet[r&31]
	}
	return string(b)
}
