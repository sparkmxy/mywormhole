package wormhole

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Options struct {
	Server      string
	InviteToken string
	StatePath   string
	Output      string
	Concurrency int
	ChunkSize   int64
	Yes         bool
	Input       io.Reader
	Log         io.Writer
}

func (o *Options) defaults() error {
	if o.Concurrency == 0 {
		o.Concurrency = 4
	}
	if o.Concurrency < 1 || o.Concurrency > 8 {
		return fmt.Errorf("parallel must be between 1 and 8")
	}
	if o.ChunkSize == 0 {
		o.ChunkSize = 16 << 20
	}
	if o.ChunkSize < 65536 || o.ChunkSize > 32<<20 {
		return fmt.Errorf("chunk size must be between 64 KiB and 32 MiB")
	}
	if o.Input == nil {
		o.Input = os.Stdin
	}
	if o.Log == nil {
		o.Log = os.Stderr
	}
	return nil
}

func Send(ctx context.Context, path string, options Options) error {
	if err := options.defaults(); err != nil {
		return err
	}
	if options.InviteToken == "" {
		return fmt.Errorf("set MYWORMHOLE_INVITE_TOKEN to the deployment invitation token before sending")
	}
	source, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Size() > 2<<30 {
		return fmt.Errorf("send expects one regular file, up to 2 GiB")
	}
	fmt.Fprintln(options.Log, "Calculating SHA-256...")
	hash, err := fileHash(source)
	if err != nil {
		return err
	}
	meta := metadata{Name: filepath.Base(source), Size: info.Size(), ChunkSize: options.ChunkSize,
		ChunkCount: int((info.Size() + options.ChunkSize - 1) / options.ChunkSize), SHA256: hash}
	if err := meta.validate(); err != nil {
		return err
	}
	a, err := newAPI(options.Server, options.InviteToken)
	if err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	var room roomInfo
	// Creation deliberately isn't retried: a lost response must not create multiple rooms.
	if err := a.request(ctx, "POST", "/v1/rooms", map[string]any{"token": token, "fileSize": meta.Size,
		"chunkSize": meta.ChunkSize, "chunkCount": meta.ChunkCount}, &room, false); err != nil {
		return err
	}
	if !validRoom(room) {
		return fmt.Errorf("invalid room response")
	}
	a.token = token
	password, err := newPassword()
	if err != nil {
		abort(a, room.Room)
		return err
	}
	fmt.Fprintf(options.Log, "Receive code: %s-%s\nWaiting for your friend...\n", room.Room, password)
	key, err := pair(ctx, a, room, password, "sender")
	if err != nil {
		abort(a, room.Room)
		return err
	}
	state := &transferState{Version: 1, Role: "sender", Server: a.base, Token: token, Room: room, Key: key, File: meta, Source: source}
	stateFile, err := statePath(options.StatePath, "sender", room.Session)
	if err != nil {
		abort(a, room.Room)
		return err
	}
	if err := saveState(stateFile, state); err != nil {
		abort(a, room.Room)
		return err
	}
	fmt.Fprintf(options.Log, "Paired securely. Resume state: %s\n", stateFile)
	return sendFile(ctx, a, state, stateFile, options)
}

func Receive(ctx context.Context, code string, options Options) error {
	if err := options.defaults(); err != nil {
		return err
	}
	roomCode, password, err := parseCode(code)
	if err != nil {
		return err
	}
	token, err := randomToken()
	if err != nil {
		return err
	}
	a, err := newAPI(options.Server, token)
	if err != nil {
		return err
	}
	var room roomInfo
	if err := a.request(ctx, "POST", "/v1/rooms/"+roomCode+"/join", map[string]string{"token": token}, &room, true); err != nil {
		return err
	}
	if !validRoom(room) || room.Room != roomCode {
		return fmt.Errorf("invalid room response")
	}
	key, err := pair(ctx, a, room, password, "receiver")
	if err != nil {
		abort(a, room.Room)
		return err
	}
	message, err := a.waitMessage(ctx, room.Room, "offer")
	if err != nil {
		return err
	}
	var meta metadata
	if err := openJSON(key, room.Session, "offer", message, &meta); err != nil {
		abort(a, room.Room)
		return err
	}
	if err := meta.validate(); err != nil {
		abort(a, room.Room)
		return err
	}
	remote, err := a.state(ctx, room.Room)
	if err != nil {
		return err
	}
	if remote.FileSize != meta.Size || remote.ChunkSize != meta.ChunkSize || remote.ChunkCount != meta.ChunkCount {
		abort(a, room.Room)
		return fmt.Errorf("server chunk plan does not match authenticated offer")
	}
	fmt.Fprintf(options.Log, "Paired securely. Offered %q (%.2f MiB).\n", meta.Name, float64(meta.Size)/(1<<20))
	if !options.Yes {
		fmt.Fprint(options.Log, "Accept? [y/N] ")
		answer, _ := bufio.NewReader(options.Input).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			abort(a, room.Room)
			return fmt.Errorf("transfer declined")
		}
	}
	output := options.Output
	if output == "" {
		output = meta.Name
	}
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(output); err == nil {
		abort(a, room.Room)
		return fmt.Errorf("output already exists; choose another --output path")
	} else if !os.IsNotExist(err) {
		return err
	}
	state := &transferState{Version: 1, Role: "receiver", Server: a.base, Token: token, Room: room, Key: key, File: meta,
		Output: output, Partial: filepath.Join(filepath.Dir(output), "."+filepath.Base(output)+"."+room.Session+".part"), Done: map[int]string{}}
	stateFile, err := statePath(options.StatePath, "receiver", room.Session)
	if err != nil {
		abort(a, room.Room)
		return err
	}
	if err := saveState(stateFile, state); err != nil {
		abort(a, room.Room)
		return err
	}
	fmt.Fprintf(options.Log, "Resume state: %s\n", stateFile)
	return receiveFile(ctx, a, state, stateFile, options)
}

func Resume(ctx context.Context, path string, options Options) error {
	if err := options.defaults(); err != nil {
		return err
	}
	state, err := loadState(path)
	if err != nil {
		return err
	}
	if time.Now().UnixMilli() >= state.Room.ExpiresAt {
		return fmt.Errorf("transfer expired; start a new send (local state contains keys; remove it when no longer needed)")
	}
	a, err := newAPI(state.Server, state.Token)
	if err != nil {
		return err
	}
	if options.Server != "" && strings.TrimRight(options.Server, "/") != a.base {
		return fmt.Errorf("resume must use the original server saved in the state")
	}
	fmt.Fprintf(options.Log, "Resuming %s for room %s...\n", state.Role, state.Room.Room)
	if state.Role == "sender" {
		return sendFile(ctx, a, state, path, options)
	}
	return receiveFile(ctx, a, state, path, options)
}

func validRoom(room roomInfo) bool {
	if len(room.Room) != 6 || len(room.Session) != 36 || room.ExpiresAt <= time.Now().UnixMilli() {
		return false
	}
	for _, c := range room.Room {
		if c < '0' || c > '9' {
			return false
		}
	}
	for _, c := range room.Session {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c == '-') {
			return false
		}
	}
	return true
}

func pair(ctx context.Context, a *api, room roomInfo, password, role string) ([]byte, error) {
	p, own, err := startPAKE(password, room.Session, role)
	if err != nil {
		return nil, err
	}
	if err := a.message(ctx, room.Room, "spake", base64.StdEncoding.EncodeToString(own)); err != nil {
		return nil, err
	}
	encoded, err := a.waitMessage(ctx, room.Room, "spake")
	if err != nil {
		return nil, err
	}
	peer, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid SPAKE2 encoding")
	}
	key, err := finishPAKE(p, peer)
	if err != nil {
		return nil, fmt.Errorf("SPAKE2 failed: %w", err)
	}
	sender, receiver := own, peer
	peerRole := "receiver"
	if role == "receiver" {
		sender, receiver = peer, own
		peerRole = "sender"
	}
	proof := confirmation(key, room.Session, role, sender, receiver)
	if err := a.message(ctx, room.Room, "confirm", base64.StdEncoding.EncodeToString(proof)); err != nil {
		return nil, err
	}
	encoded, err = a.waitMessage(ctx, room.Room, "confirm")
	if err != nil {
		return nil, err
	}
	proof, err = base64.StdEncoding.DecodeString(encoded)
	if err != nil || !hmac.Equal(proof, confirmation(key, room.Session, peerRole, sender, receiver)) {
		return nil, fmt.Errorf("PAKE key confirmation failed: wrong code or active interference; create a new code")
	}
	return key, nil
}

func abort(a *api, room string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = a.request(ctx, "POST", "/v1/rooms/"+room+"/abort", map[string]bool{}, nil, false)
}
func sendSealedOnce(ctx context.Context, a *api, state *transferState, name string, value any) error {
	remote, err := a.state(ctx, state.Room.Room)
	if err != nil {
		return err
	}
	if remote.Session != state.Room.Session {
		return fmt.Errorf("session changed")
	}
	if remote.Own[name] != "" {
		return nil
	}
	encrypted, err := sealJSON(state.Key, state.Room.Session, name, value)
	if err != nil {
		return err
	}
	return a.message(ctx, state.Room.Room, name, encrypted)
}

func sendFile(ctx context.Context, a *api, state *transferState, stateFile string, options Options) error {
	hash, err := fileHash(state.Source)
	if err != nil {
		return err
	}
	if hash != state.File.SHA256 {
		return fmt.Errorf("source changed; start a new send to avoid mixing file versions")
	}
	if err := sendSealedOnce(ctx, a, state, "offer", state.File); err != nil {
		return err
	}
	accepted, err := a.waitMessage(ctx, state.Room.Room, "accept")
	if err != nil {
		return err
	}
	var acceptance struct {
		SHA256 string `json:"sha256"`
	}
	if err := openJSON(state.Key, state.Room.Session, "accept", accepted, &acceptance); err != nil {
		return err
	}
	if acceptance.SHA256 != state.File.SHA256 {
		return fmt.Errorf("receiver accepted a different file")
	}
	remote, err := a.state(ctx, state.Room.Room)
	if err != nil {
		return err
	}
	ready := map[int]bool{}
	for _, i := range remote.Ready {
		if i >= 0 && i < state.File.ChunkCount {
			ready[i] = true
		}
	}
	file, err := os.Open(state.Source)
	if err != nil {
		return err
	}
	defer file.Close()
	progress := newProgress(options.Log, "Uploaded", state.File.Size)
	err = parallel(ctx, state.File.ChunkCount, options.Concurrency, func(ctx context.Context, index int) error {
		length := state.File.length(index)
		if ready[index] {
			progress.add(length)
			return nil
		}
		plain := make([]byte, int(length))
		if _, err := file.ReadAt(plain, int64(index)*state.File.ChunkSize); err != nil {
			return err
		}
		encrypted, err := seal(state.Key, state.Room.Session, fmt.Sprintf("chunk/%d", index), plain)
		if err != nil {
			return err
		}
		for attempt := 0; attempt < 5; attempt++ {
			if attempt > 0 {
				if err := pause(ctx, time.Duration(1<<uint(attempt-1))*time.Second); err != nil {
					return err
				}
			}
			var g grant
			g, err = a.objectGrant(ctx, state.Room.Room, index, "PUT")
			if err == nil {
				_, err = a.objectRequest(ctx, "PUT", g, encrypted, 4096)
			}
			if err == nil {
				err = a.request(ctx, "POST", fmt.Sprintf("/v1/rooms/%s/ready/%d", state.Room.Room, index), map[string]bool{}, nil, true)
			}
			if err == nil {
				progress.add(length)
				return nil
			}
			if !retryable(err) {
				break
			}
		}
		return fmt.Errorf("upload chunk %d: %w", index, err)
	})
	if err != nil {
		return fmt.Errorf("%w; resume with: mywormhole resume %q", err, stateFile)
	}
	progress.finish()
	fmt.Fprintln(options.Log, "Waiting for receiver integrity check...")
	receipt, err := a.waitMessage(ctx, state.Room.Room, "done")
	if err != nil {
		return err
	}
	var done struct {
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	if err := openJSON(state.Key, state.Room.Session, "done", receipt, &done); err != nil {
		return err
	}
	if done.SHA256 != state.File.SHA256 || done.Size != state.File.Size {
		return fmt.Errorf("invalid receiver receipt")
	}
	if err := a.request(ctx, "DELETE", "/v1/rooms/"+state.Room.Room, nil, nil, true); err != nil {
		fmt.Fprintln(options.Log, "Receiver verified the file. Immediate cloud cleanup failed; automatic expiry will retry.")
	} else {
		fmt.Fprintln(options.Log, "Receiver verified SHA-256. Cloud chunks deleted.")
	}
	_ = os.Remove(stateFile)
	return nil
}

func receiveFile(ctx context.Context, a *api, state *transferState, stateFile string, options Options) error {
	// Recover the crash window between publishing the file and persisting Published.
	if _, err := os.Lstat(state.Output); err == nil {
		hash, err := fileHash(state.Output)
		if err != nil || hash != state.File.SHA256 {
			return fmt.Errorf("output already exists and differs from the offered file")
		}
		state.Published = true
	}
	if !state.Published {
		if info, err := os.Lstat(state.Partial); err == nil && !info.Mode().IsRegular() {
			return fmt.Errorf("partial output must be a regular file")
		}
		file, err := os.OpenFile(state.Partial, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return err
		}
		defer file.Close()
		if err := file.Truncate(state.File.Size); err != nil {
			return err
		}
		// Validate completed local chunks before trusting a saved bitmap.
		for index, want := range state.Done {
			if index < 0 || index >= state.File.ChunkCount {
				delete(state.Done, index)
				continue
			}
			data := make([]byte, int(state.File.length(index)))
			_, readErr := file.ReadAt(data, int64(index)*state.File.ChunkSize)
			sum := sha256.Sum256(data)
			if readErr != nil || hex.EncodeToString(sum[:]) != want {
				delete(state.Done, index)
			}
		}
		if err := sendSealedOnce(ctx, a, state, "accept", map[string]string{"sha256": state.File.SHA256}); err != nil {
			return err
		}
		var stateMu sync.Mutex
		progress := newProgress(options.Log, "Received", state.File.Size)
		err = parallel(ctx, state.File.ChunkCount, options.Concurrency, func(ctx context.Context, index int) error {
			length := state.File.length(index)
			stateMu.Lock()
			_, complete := state.Done[index]
			stateMu.Unlock()
			if complete {
				progress.add(length)
				return nil
			}
			for {
				remote, err := a.state(ctx, state.Room.Room)
				if err != nil {
					return err
				}
				if remote.Session != state.Room.Session {
					return fmt.Errorf("session changed")
				}
				ready := false
				for _, v := range remote.Ready {
					if v == index {
						ready = true
						break
					}
				}
				if ready {
					break
				}
				if err := pause(ctx, time.Second); err != nil {
					return err
				}
			}
			var encrypted []byte
			var err error
			for attempt := 0; attempt < 5; attempt++ {
				if attempt > 0 {
					if err := pause(ctx, time.Duration(1<<uint(attempt-1))*time.Second); err != nil {
						return err
					}
				}
				var g grant
				g, err = a.objectGrant(ctx, state.Room.Room, index, "GET")
				if err == nil {
					encrypted, err = a.objectRequest(ctx, "GET", g, nil, length+28)
				}
				if err == nil {
					break
				}
				if !retryable(err) {
					return err
				}
			}
			if err != nil {
				return fmt.Errorf("download chunk %d: %w", index, err)
			}
			plain, err := open(state.Key, state.Room.Session, fmt.Sprintf("chunk/%d", index), encrypted)
			if err != nil {
				return fmt.Errorf("chunk %d authentication failed: cloud data was modified", index)
			}
			if int64(len(plain)) != length {
				return fmt.Errorf("chunk %d length mismatch", index)
			}
			if _, err := file.WriteAt(plain, int64(index)*state.File.ChunkSize); err != nil {
				return err
			}
			if err := file.Sync(); err != nil {
				return err
			}
			sum := sha256.Sum256(plain)
			stateMu.Lock()
			state.Done[index] = hex.EncodeToString(sum[:])
			err = saveState(stateFile, state)
			stateMu.Unlock()
			if err != nil {
				return err
			}
			progress.add(length)
			return nil
		})
		if err != nil {
			return fmt.Errorf("%w; resume with: mywormhole resume %q", err, stateFile)
		}
		progress.finish()
		if err := file.Close(); err != nil {
			return err
		}
		hash, err := fileHash(state.Partial)
		if err != nil {
			return err
		}
		if hash != state.File.SHA256 {
			return fmt.Errorf("whole-file SHA-256 mismatch; partial file retained")
		}
		// Hard-link publication is atomic and refuses to overwrite an existing path on all supported OSes.
		if err := os.Link(state.Partial, state.Output); err != nil {
			return fmt.Errorf("publish without overwriting output: %w (use an NTFS/APFS/ext4 directory)", err)
		}
		state.Published = true
		if err := saveState(stateFile, state); err != nil {
			return err
		}
		_ = os.Remove(state.Partial)
	}
	// Also remove a leftover hard link when resuming after a publish/state-save crash.
	_ = os.Remove(state.Partial)
	if err := sendSealedOnce(ctx, a, state, "done", map[string]any{"sha256": state.File.SHA256, "size": state.File.Size}); err != nil {
		return err
	}
	_ = os.Remove(stateFile)
	fmt.Fprintf(options.Log, "Saved %s\nSHA-256 verified: %s\n", state.Output, state.File.SHA256)
	return nil
}

func parallel(ctx context.Context, count, workers int, fn func(context.Context, int) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	jobs := make(chan int)
	errors := make(chan error, 1)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range jobs {
				if err := fn(ctx, index); err != nil {
					select {
					case errors <- err:
						cancel()
					default:
					}
					return
				}
			}
		}()
	}
dispatch:
	for index := 0; index < count; index++ {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break dispatch
		}
	}
	close(jobs)
	wg.Wait()
	select {
	case err := <-errors:
		return err
	default:
		return ctx.Err()
	}
}

type progress struct {
	mu          sync.Mutex
	writer      io.Writer
	label       string
	total, done int64
	start, last time.Time
}

func newProgress(writer io.Writer, label string, total int64) *progress {
	return &progress{writer: writer, label: label, total: total, start: time.Now()}
}
func (p *progress) add(bytes int64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.done += bytes
	if time.Since(p.last) >= time.Second {
		p.print()
		p.last = time.Now()
	}
}
func (p *progress) print() {
	seconds := time.Since(p.start).Seconds()
	if seconds < 0.001 {
		seconds = 0.001
	}
	fmt.Fprintf(p.writer, "%s %.1f / %.1f MiB (%.2f MiB/s)\n", p.label, float64(p.done)/(1<<20), float64(p.total)/(1<<20), float64(p.done)/(1<<20)/seconds)
}
func (p *progress) finish() { p.mu.Lock(); defer p.mu.Unlock(); p.print() }
