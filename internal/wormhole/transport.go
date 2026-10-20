package wormhole

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type api struct {
	base, token string
	client      *http.Client
}
type roomInfo struct {
	Room      string `json:"room"`
	Session   string `json:"session"`
	ExpiresAt int64  `json:"expiresAt"`
}
type roomState struct {
	roomInfo
	Own        map[string]string `json:"own"`
	Peer       map[string]string `json:"peer"`
	Ready      []int             `json:"ready"`
	FileSize   int64             `json:"fileSize"`
	ChunkSize  int64             `json:"chunkSize"`
	ChunkCount int               `json:"chunkCount"`
}
type grant struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("server returned %d: %s", e.Status, e.Message) }

func newAPI(server, token string) (*api, error) {
	u, err := url.Parse(server)
	if err != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("server must be an HTTPS origin, for example https://mywormhole.example.workers.dev")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || net.ParseIP(u.Hostname()).IsLoopback())) {
		return nil, fmt.Errorf("HTTP is only allowed on loopback for local development")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 16
	transport.MaxConnsPerHost = 16
	transport.ResponseHeaderTimeout = 60 * time.Second
	return &api{base: strings.TrimRight(server, "/"), token: token, client: &http.Client{
		Transport: transport, Timeout: 10 * time.Minute,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (a *api) request(ctx context.Context, method, path string, value, out any, retry bool) error {
	var data []byte
	var err error
	if value != nil {
		data, err = json.Marshal(value)
		if err != nil {
			return err
		}
	}
	attempts := 1
	if retry {
		attempts = 5
	}
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			if err := pause(ctx, time.Duration(1<<uint(attempt-1))*250*time.Millisecond); err != nil {
				return err
			}
		}
		request, err := http.NewRequestWithContext(ctx, method, a.base+path, bytes.NewReader(data))
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+a.token)
		request.Header.Set("Content-Type", "application/json")
		response, err := a.client.Do(request)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if attempt+1 < attempts {
				continue
			}
			return fmt.Errorf("control request failed; check the server address and connection")
		}
		data, readErr := io.ReadAll(io.LimitReader(response.Body, 131073))
		response.Body.Close()
		if response.StatusCode >= 500 || response.StatusCode == 429 {
			if attempt+1 < attempts {
				continue
			}
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			var problem struct {
				Error string `json:"error"`
			}
			_ = json.Unmarshal(data, &problem)
			if problem.Error == "" {
				problem.Error = http.StatusText(response.StatusCode)
			}
			return &apiError{response.StatusCode, problem.Error}
		}
		if readErr != nil {
			return readErr
		}
		if len(data) > 131072 {
			return fmt.Errorf("server response too large")
		}
		if out != nil {
			return json.Unmarshal(data, out)
		}
		return nil
	}
	return fmt.Errorf("request attempts exhausted")
}

func (a *api) state(ctx context.Context, room string) (roomState, error) {
	var state roomState
	err := a.request(ctx, "GET", "/v1/rooms/"+room+"/state", nil, &state, true)
	return state, err
}
func (a *api) message(ctx context.Context, room, name, data string) error {
	return a.request(ctx, "POST", "/v1/rooms/"+room+"/message", map[string]string{"name": name, "data": data}, nil, true)
}
func (a *api) waitMessage(ctx context.Context, room, name string) (string, error) {
	for {
		state, err := a.state(ctx, room)
		if err != nil {
			return "", err
		}
		if message := state.Peer[name]; message != "" {
			return message, nil
		}
		if err := pause(ctx, time.Second); err != nil {
			return "", err
		}
	}
}
func (a *api) objectGrant(ctx context.Context, room string, index int, method string) (grant, error) {
	var result grant
	err := a.request(ctx, "POST", "/v1/rooms/"+room+"/url", map[string]any{"index": index, "method": method}, &result, true)
	if err != nil {
		return result, err
	}
	u, err := url.Parse(result.URL)
	if err != nil {
		return result, fmt.Errorf("invalid object URL")
	}
	if !u.IsAbs() {
		if !strings.HasPrefix(result.URL, "/v1/rooms/"+room+"/objects/") || strings.HasPrefix(result.URL, "//") {
			return result, fmt.Errorf("invalid relative object URL")
		}
		result.URL = a.base + result.URL
	} else {
		// Never forward a room bearer token to a different origin, even if a server asks.
		if u.Scheme != "https" || !strings.HasSuffix(u.Hostname(), ".r2.cloudflarestorage.com") || u.User != nil {
			return result, fmt.Errorf("unexpected direct storage endpoint")
		}
		for name := range result.Headers {
			if strings.EqualFold(name, "Authorization") {
				return result, fmt.Errorf("unexpected storage authorization header")
			}
		}
	}
	return result, nil
}

func (a *api) objectRequest(ctx context.Context, method string, grant grant, payload []byte, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, method, grant.URL, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("invalid storage request")
	}
	for name, value := range grant.Headers {
		if !strings.EqualFold(name, "Content-Length") {
			request.Header.Set(name, value)
		}
	}
	response, err := a.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("storage connection failed") // Do not log presigned URL credentials.
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return nil, &apiError{response.StatusCode, "storage request failed"}
	}
	result, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(result)) > limit {
		return nil, fmt.Errorf("storage response exceeds expected length")
	}
	return result, nil
}

func pause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func retryable(err error) bool {
	var response *apiError
	if errors.As(err, &response) {
		return response.Status >= 500 || response.Status == 429 || response.Status == 403 || response.Status == 404
	}
	return true
}
