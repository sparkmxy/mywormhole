package wormhole

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

type metadata struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ChunkSize  int64  `json:"chunkSize"`
	ChunkCount int    `json:"chunkCount"`
	SHA256     string `json:"sha256"`
}
type transferState struct {
	Version   int            `json:"version"`
	Role      string         `json:"role"`
	Server    string         `json:"server"`
	Token     string         `json:"token"`
	Room      roomInfo       `json:"room"`
	Key       []byte         `json:"key"`
	File      metadata       `json:"file"`
	Source    string         `json:"source,omitempty"`
	Output    string         `json:"output,omitempty"`
	Partial   string         `json:"partial,omitempty"`
	Done      map[int]string `json:"done,omitempty"`
	Published bool           `json:"published,omitempty"`
}

func (m metadata) validate() error {
	for _, character := range m.Name {
		if unicode.IsControl(character) {
			return fmt.Errorf("control characters in offered filename")
		}
	}
	if m.Name == "" || m.Name == "." || m.Name == ".." || strings.ContainsAny(m.Name, "/\\:\x00") || len(m.Name) > 240 {
		return fmt.Errorf("unsafe offered filename")
	}
	if m.Size < 0 || m.Size > 2<<30 || m.ChunkSize < 65536 || m.ChunkSize > 32<<20 || m.ChunkCount < 0 || m.ChunkCount > 2048 ||
		int64(m.ChunkCount) != (m.Size+m.ChunkSize-1)/m.ChunkSize {
		return fmt.Errorf("invalid offered file size or chunk plan")
	}
	hash, err := hex.DecodeString(m.SHA256)
	if err != nil || len(hash) != 32 {
		return fmt.Errorf("invalid offered SHA-256")
	}
	return nil
}
func (m metadata) length(index int) int64 {
	remaining := m.Size - int64(index)*m.ChunkSize
	if remaining < m.ChunkSize {
		return remaining
	}
	return m.ChunkSize
}
func fileHash(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func saveState(path string, state *transferState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".mywormhole-state-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(name, path)
}
func loadState(path string) (*transferState, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var state transferState
	if err := json.NewDecoder(io.LimitReader(file, 1<<20)).Decode(&state); err != nil {
		return nil, err
	}
	if state.Version != 1 || (state.Role != "sender" && state.Role != "receiver") || len(state.Key) != 32 || len(state.Token) != 43 ||
		len(state.Room.Room) != 6 || len(state.Room.Session) != 36 {
		return nil, fmt.Errorf("invalid resume state")
	}
	if err := state.File.validate(); err != nil {
		return nil, err
	}
	if state.Done == nil {
		state.Done = map[int]string{}
	}
	return &state, nil
}
func statePath(explicit, role, session string) (string, error) {
	if explicit != "" {
		path, err := filepath.Abs(explicit)
		if err != nil {
			return "", err
		}
		if _, err := os.Stat(path); err == nil {
			return "", fmt.Errorf("state file already exists; use --resume or choose another --state path")
		} else if !os.IsNotExist(err) {
			return "", err
		}
		return path, nil
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "mywormhole", role+"-"+session+".json"), nil
}
