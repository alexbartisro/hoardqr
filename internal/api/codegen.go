package api

import "math/rand"

// plainTextCodeAlphabet is Crockford's Base32 alphabet minus I, L, O, U —
// chosen (architecture plan §4) to avoid characters that are visually
// ambiguous on a hand-written sticky note or a plain text-only label.
// Mirrors web/src/lib/api.ts's generatePlainTextCode() exactly.
const plainTextCodeAlphabet = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

func generatePlainTextCode() string {
	b := make([]byte, 6)
	for i := range b {
		b[i] = plainTextCodeAlphabet[rand.Intn(len(plainTextCodeAlphabet))]
	}
	return string(b)
}
