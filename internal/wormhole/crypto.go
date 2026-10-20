package wormhole

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"

	"salsa.debian.org/vasudev/gospake2"
)

// We use the existing SPAKE2 implementation used by wormhole-william.
// Protocol/domain separation here is specific to mywormhole, not wire-compatible
// with Magic Wormhole. The PAKE algebra is entirely inside the dependency.
func startPAKE(password, session, role string) (*gospake2.SPAKE2, []byte, error) {
	pw := gospake2.NewPassword(password)
	a := gospake2.NewIdentityA("mywormhole/v1/" + session + "/sender")
	b := gospake2.NewIdentityB("mywormhole/v1/" + session + "/receiver")
	var p gospake2.SPAKE2
	if role == "sender" {
		p = gospake2.SPAKE2A(pw, a, b)
	} else {
		p = gospake2.SPAKE2B(pw, a, b)
	}
	return &p, p.Start(), nil
}

func finishPAKE(p *gospake2.SPAKE2, message []byte) (key []byte, err error) {
	// Guard the older dependency at its untrusted-input boundary.
	defer func() {
		if recover() != nil {
			key = nil
			err = fmt.Errorf("invalid SPAKE2 peer message")
		}
	}()
	if len(message) != 33 || (message[0] != 'A' && message[0] != 'B') {
		return nil, fmt.Errorf("invalid SPAKE2 message length or role")
	}
	return p.Finish(message)
}

// HKDF-SHA256 (RFC 5869), with separate labels for confirmation, metadata and data.
// This is the standard extract/expand construction; it does not replace PAKE.
func derive(shared []byte, session, label string) []byte {
	salt := sha256.Sum256([]byte("mywormhole/v1/" + session))
	key, err := hkdf.Key(sha256.New, shared, salt[:], "mywormhole/v1/"+label, 32)
	if err != nil {
		panic(err)
	} // Fixed output length is well below the HKDF bound.
	return key
}

func confirmation(shared []byte, session, role string, sender, receiver []byte) []byte {
	m := hmac.New(sha256.New, derive(shared, session, "confirmation/"+role))
	m.Write([]byte("mywormhole/v1/" + session + "/" + role + "\x00"))
	m.Write(sender)
	m.Write(receiver)
	return m.Sum(nil)
}

func newAEAD(key []byte, session, label string) (cipher.AEAD, error) {
	block, err := aes.NewCipher(derive(key, session, label))
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func seal(key []byte, session, label string, plain []byte) ([]byte, error) {
	aead, err := newAEAD(key, session, label)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plain, []byte(session+"/"+label)), nil
}

func open(key []byte, session, label string, encrypted []byte) ([]byte, error) {
	aead, err := newAEAD(key, session, label)
	if err != nil {
		return nil, err
	}
	if len(encrypted) < aead.NonceSize()+aead.Overhead() {
		return nil, fmt.Errorf("truncated ciphertext")
	}
	return aead.Open(nil, encrypted[:aead.NonceSize()], encrypted[aead.NonceSize():], []byte(session+"/"+label))
}

func sealJSON(key []byte, session, label string, value any) (string, error) {
	plain, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	encrypted, err := seal(key, session, label, plain)
	return base64.StdEncoding.EncodeToString(encrypted), err
}

func openJSON(key []byte, session, label, value string, out any) error {
	encrypted, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return err
	}
	plain, err := open(key, session, label, encrypted)
	if err != nil {
		return fmt.Errorf("%s authentication failed (wrong code or modified data)", label)
	}
	return json.Unmarshal(plain, out)
}
