package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// deleteBatch is how many paths go into one deletion request.
const deleteBatch = 200

// historyLength is how many runs the page lists.
const historyLength = 30

// Settings is the module's configuration.
type Settings struct {
	CoreURL      string `json:"core_url"`
	APIToken     string `json:"api_token"`
	AutoInterval string `json:"auto_interval"`

	interval time.Duration
}

var errSettings = errors.New("the cleanup settings are not usable")

func (s *Settings) check() error {
	every, err := time.ParseDuration(s.AutoInterval)
	if err != nil || (every != 0 && every < 10*time.Minute) {
		return fmt.Errorf("%w: auto_interval is %q and has to be 0 (off) or at least 10m", errSettings, s.AutoInterval)
	}

	s.interval = every
	s.CoreURL = strings.TrimRight(s.CoreURL, "/")

	if s.CoreURL != "" {
		if u, err := url.Parse(s.CoreURL); err != nil || u.Host == "" {
			return fmt.Errorf("%w: core_url is not an address", errSettings)
		}
	}

	return nil
}

// automatic reports whether the module deletes on its own schedule.
func (s Settings) automatic() bool {
	return s.interval > 0 && s.APIToken != "" && s.CoreURL != ""
}

// RunRecord is one cleanup, by hand or on the schedule.
type RunRecord struct {
	At      time.Time `json:"at"`
	By      string    `json:"by"`
	Rule    string    `json:"rule"`
	Units   int       `json:"units"`
	Files   int       `json:"files"`
	Freed   int64     `json:"freed"`
	Error   string    `json:"error,omitempty"`
	Partial bool      `json:"partial,omitempty"`
}

type history struct {
	mu   sync.Mutex
	path string
	runs []RunRecord
}

func loadHistory(path string) *history {
	h := &history{path: path}

	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &h.runs)
	}

	return h
}

func (h *history) add(r RunRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.runs = append([]RunRecord{r}, h.runs...)
	if len(h.runs) > historyLength {
		h.runs = h.runs[:historyLength]
	}

	if raw, err := json.MarshalIndent(h.runs, "", "  "); err == nil {
		_ = os.WriteFile(h.path, raw, 0o640)
	}
}

func (h *history) list() []RunRecord {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]RunRecord{}, h.runs...)
}

// deleter removes entries through the server's own HTTP API, with the token
// the operator gave the module. The module protocol can write and read files
// but not delete them, so this is the only way to delete with nobody on the
// page.
type deleter struct {
	base   string
	token  string
	client *http.Client
}

func (d deleter) remove(ctx context.Context, storage string, paths []string) (int, error) {
	done := 0

	for start := 0; start < len(paths); start += deleteBatch {
		batch := paths[start:min(start+deleteBatch, len(paths))]

		body, _ := json.Marshal(map[string]any{"paths": batch})
		address := d.base + "/api/v1/storages/" + url.PathEscape(storage) + "/deletions"

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
		if err != nil {
			return done, err
		}

		req.Header.Set("Authorization", "Bearer "+d.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := d.client.Do(req)
		if err != nil {
			return done, err
		}

		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()

		if resp.StatusCode/100 != 2 {
			return done, fmt.Errorf("the server answered %s: %s", resp.Status, strings.TrimSpace(string(detail)))
		}

		done += len(batch)
	}

	return done, nil
}
