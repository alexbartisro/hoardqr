package codegen

import (
	"strings"
	"testing"
)

func TestPlainTextCodeShapeAndAlphabet(t *testing.T) {
	if len(PlainTextCodeAlphabet) != 32 {
		t.Fatalf("alphabet must be exactly 32 chars for the &31 mask to be unbiased, got %d", len(PlainTextCodeAlphabet))
	}
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		c := PlainTextCode()
		if len(c) != 6 {
			t.Fatalf("code %q: want length 6", c)
		}
		for _, r := range c {
			if !strings.ContainsRune(PlainTextCodeAlphabet, r) {
				t.Fatalf("code %q has %q outside the alphabet", c, r)
			}
		}
		seen[c] = true
	}
	if len(seen) < 495 {
		t.Fatalf("only %d distinct codes out of 500 — generator looks non-random", len(seen))
	}
}
