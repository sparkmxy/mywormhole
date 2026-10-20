package wormhole

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLargeFileMetadata(t *testing.T) {
	const chunkSize int64 = 16 << 20
	for _, tc := range []struct {
		name string
		size int64
		want bool
	}{
		{"empty", 0, true},
		{"old limit", 2 << 30, true},
		{"above old limit", 2<<30 + 1, true},
		{"above 32-bit offset", 4<<30 + 1, true},
		{"10 GB decimal", 10_000_000_000, true},
		{"10 GiB exact", 10 << 30, true},
		{"one byte over", 10<<30 + 1, false},
		{"negative", -1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := metadata{Name: "large.bin", Size: tc.size, ChunkSize: chunkSize,
				ChunkCount: int((tc.size + chunkSize - 1) / chunkSize), SHA256: strings.Repeat("a", 64)}
			if err := m.validate(); (err == nil) != tc.want {
				t.Fatalf("validate size %d: %v (want accepted=%v)", tc.size, err, tc.want)
			}
			if tc.want && m.ChunkCount > 0 {
				last := m.ChunkCount - 1
				if got := int64(last)*chunkSize + m.length(last); got != tc.size {
					t.Fatalf("last chunk ends at %d, want %d", got, tc.size)
				}
			}
		})
	}
}

func TestLargeFileResumeState(t *testing.T) {
	const size int64 = 10 << 30
	const chunkSize int64 = 16 << 20
	state := &transferState{Version: 1, Role: "receiver", Token: strings.Repeat("r", 43),
		Room: roomInfo{Room: "123456", Session: "12345678-1234-1234-1234-123456789abc"},
		Key:  make([]byte, 32), File: metadata{Name: "large.bin", Size: size, ChunkSize: chunkSize,
			ChunkCount: 640, SHA256: strings.Repeat("a", 64)},
		Done: map[int]string{639: strings.Repeat("b", 64)}}
	path := filepath.Join(t.TempDir(), "resume.json")
	if err := saveState(path, state); err != nil {
		t.Fatal(err)
	}
	got, err := loadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.File != state.File || got.Done[639] != state.Done[639] {
		t.Fatal("large file plan or high chunk index changed on resume")
	}
	state.File.ChunkSize = 1 << 20
	state.File.ChunkCount = 10240
	if err := state.File.validate(); err == nil {
		t.Fatal("more than 2048 chunks accepted")
	}
}
