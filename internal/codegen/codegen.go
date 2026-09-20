// Package codegen generates the plain-text storage/item codes (architecture
// plan §4). Shared between internal/api (REST create endpoints) and
// internal/mcpserver (the add_item/add_storage MCP tools) so both create
// paths produce identically-shaped codes without duplicating the alphabet.
package codegen

import "math/rand"

// PlainTextCodeAlphabet is Crockford's Base32 alphabet minus I, L, O, U —
// chosen (architecture plan §4) to avoid characters that are visually
// ambiguous on a hand-written sticky note or a plain text-only label.
// Mirrors web/src/lib/api.ts's generatePlainTextCode() exactly.
const PlainTextCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// PlainTextCode generates a new 6-character code from PlainTextCodeAlphabet.
func PlainTextCode() string {
	b := make([]byte, 6)
	for i := range b {
		b[i] = PlainTextCodeAlphabet[rand.Intn(len(PlainTextCodeAlphabet))]
	}
	return string(b)
}
