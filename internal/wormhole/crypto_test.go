package wormhole

import (
	"bytes"
	"crypto/hmac"
	"encoding/base64"
	"strings"
	"testing"
)

func TestSPAKE2AndMutualConfirmation(t *testing.T) {
	a, x, err := startPAKE("amber-brook-cloud-river", "test-session", "sender")
	if err != nil {
		t.Fatal(err)
	}
	b, y, err := startPAKE("amber-brook-cloud-river", "test-session", "receiver")
	if err != nil {
		t.Fatal(err)
	}
	ka, err := finishPAKE(a, y)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := finishPAKE(b, x)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ka, kb) {
		t.Fatal("keys differ")
	}
	if !hmac.Equal(confirmation(ka, "test-session", "sender", x, y), confirmation(kb, "test-session", "sender", x, y)) {
		t.Fatal("confirmation differs")
	}
	if hmac.Equal(confirmation(ka, "test-session", "sender", x, y), confirmation(kb, "test-session", "receiver", x, y)) {
		t.Fatal("role reflection accepted")
	}
	value, err := sealJSON(ka, "test-session", "offer", map[string]string{"name": "文件.zip"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := openJSON(kb, "test-session", "offer", value, &got); err != nil || got["name"] != "文件.zip" {
		t.Fatalf("roundtrip: %v %#v", err, got)
	}
}

func TestWrongPasswordAndSessionCannotConfirm(t *testing.T) {
	for _, tc := range []struct{ name, password, session string }{
		{"password", "wrong-password", "same-session"}, {"session", "right-password", "another-session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, x, _ := startPAKE("right-password", "same-session", "sender")
			b, y, _ := startPAKE(tc.password, tc.session, "receiver")
			ka, err := finishPAKE(a, y)
			if err != nil {
				return
			}
			kb, err := finishPAKE(b, x)
			if err != nil {
				return
			}
			if bytes.Equal(ka, kb) {
				t.Fatal("wrong password/context produced identical keys")
			}
			if hmac.Equal(confirmation(ka, "same-session", "sender", x, y), confirmation(kb, tc.session, "sender", x, y)) {
				t.Fatal("wrong secret authenticated")
			}
		})
	}
}

func TestCiphertextRejectsModificationAndChunkSwap(t *testing.T) {
	key := bytes.Repeat([]byte{42}, 32)
	encrypted, err := seal(key, "session", "chunk/0", []byte("private file contents"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open(key, "session", "chunk/1", encrypted); err == nil {
		t.Fatal("chunk swap accepted")
	}
	if _, err := open(key, "different-session", "chunk/0", encrypted); err == nil {
		t.Fatal("session replay accepted")
	}
	encrypted[len(encrypted)-1] ^= 1
	if _, err := open(key, "session", "chunk/0", encrypted); err == nil {
		t.Fatal("corruption accepted")
	}
	if _, err := open(key, "session", "chunk/0", []byte{1}); err == nil {
		t.Fatal("truncation accepted")
	}
}

func TestMalformedSPAKEMessages(t *testing.T) {
	for _, data := range [][]byte{nil, {1}, bytes.Repeat([]byte{255}, 33), append([]byte{'B'}, make([]byte, 32)...)} {
		a, _, _ := startPAKE("password", "session", "sender")
		_, _ = finishPAKE(a, data) // Must never panic on a relay-controlled message.
	}
	a, _, _ := startPAKE("password", "session", "sender")
	_, y, _ := startPAKE("password", "session", "receiver")
	y[0] = 'Z'
	if _, err := finishPAKE(a, y); err == nil {
		t.Fatal("unknown SPAKE2 role accepted")
	}
}

func TestCodeFormatAndWordList(t *testing.T) {
	if len(words) != 64 {
		t.Fatalf("word list has %d words", len(words))
	}
	seen := map[string]bool{}
	for _, word := range words {
		if seen[word] {
			t.Fatal("duplicate word", word)
		}
		seen[word] = true
	}
	password, err := newPassword()
	if err != nil {
		t.Fatal(err)
	}
	room, got, err := parseCode("123456-" + password)
	if err != nil || room != "123456" || got != password {
		t.Fatal("code roundtrip", err)
	}
	for _, bad := range []string{"123-amber-brook-cloud-river", "123456-amber-brook-cloud-wrong", "../../bad", "123456-amber"} {
		if _, _, err := parseCode(bad); err == nil {
			t.Fatal("bad code accepted", bad)
		}
	}
	if token, err := randomToken(); err != nil || len(token) != 43 {
		t.Fatal("token", err)
	} else if raw, err := base64.RawURLEncoding.DecodeString(token); err != nil || len(raw) != 32 {
		t.Fatal("token entropy")
	}
}

func TestMetadataRejectsTraversalAndOversize(t *testing.T) {
	valid := metadata{Name: "normal.txt", Size: 1, ChunkSize: 65536, ChunkCount: 1, SHA256: strings.Repeat("a", 64)}
	if err := valid.validate(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../escape", "..", "x\\evil", "C:escape", "nul\x00file", "terminal\x1b[2J.txt", "line\nbreak.txt"} {
		m := valid
		m.Name = name
		if err := m.validate(); err == nil {
			t.Fatal("unsafe name accepted", name)
		}
	}
	valid.Size = 10<<30 + 1
	if err := valid.validate(); err == nil {
		t.Fatal("oversize accepted")
	}
}
