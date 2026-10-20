package wormhole

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
)

// 64 distinct, easy-to-type words. Four independent words provide 24 secret
// bits; the six-digit room locator is public and contributes no secret bits.
var words = strings.Fields(`amber apple arrow atlas beach berry birch bloom
blue boat brave bread brick brook brush cedar cherry cloud coral crane
creek dawn deer dune eagle earth fern field flame forest frost globe
gold grape green grove hill honey iris jade lake leaf lemon light
lime lotus maple moon moss ocean olive orange panda pearl pine plum
pond raven reed river robin rose silver star`)

func newPassword() (string, error) {
	if len(words) != 64 {
		return "", fmt.Errorf("invalid embedded word list")
	}
	b := make([]byte, 4)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	selected := make([]string, len(b))
	for i, v := range b {
		selected[i] = words[int(v)&63]
	}
	return strings.Join(selected, "-"), nil
}

func parseCode(code string) (room, password string, err error) {
	parts := strings.Split(strings.TrimSpace(strings.ToLower(code)), "-")
	if len(parts) != 5 || len(parts[0]) != 6 {
		return "", "", fmt.Errorf("expected a code such as 483921-amber-brook-cloud-river")
	}
	for _, c := range parts[0] {
		if c < '0' || c > '9' {
			return "", "", fmt.Errorf("invalid room locator")
		}
	}
	for _, part := range parts[1:] {
		found := false
		for _, word := range words {
			if part == word {
				found = true
				break
			}
		}
		if !found {
			return "", "", fmt.Errorf("unknown word in code: %q", part)
		}
	}
	return parts[0], strings.Join(parts[1:], "-"), nil
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
