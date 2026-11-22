package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestCoreURLHasToPointAtThisMachine(t *testing.T) {
	for raw, want := range map[string]string{
		"http://127.0.0.1:8080":  "http://127.0.0.1:8080",
		"http://127.0.0.1:8087/": "http://127.0.0.1:8087",
		"http://localhost:9000":  "http://localhost:9000",
		"http://LOCALHOST":       "http://LOCALHOST",
		"http://[::1]:8080":      "http://[::1]:8080",
		"https://127.0.0.1":      "https://127.0.0.1",
	} {
		got, err := checkCoreURL(raw)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v", raw, got, err)
		}
	}

	for _, raw := range []string{
		"http://192.0.2.10:8080",
		"http://example.com:8080",
		"http://localhost.example.com",
		"http://127.0.0.1.example.com",
		"ftp://127.0.0.1",
		"http://user:secret@127.0.0.1:8080",
		"http://127.0.0.1:8080/?x=1",
		"http://127.0.0.1:8080/#x",
		"127.0.0.1:8080",
		"not an address",
	} {
		if _, err := checkCoreURL(raw); !errors.Is(err, errSettings) {
			t.Errorf("%s was accepted (%v)", raw, err)
		}
	}
}

func TestSettingsRefuseACoreURLElsewhere(t *testing.T) {
	s := Settings{AutoInterval: "0s", CoreURL: "http://192.0.2.10:8080"}
	if err := s.check(); !errors.Is(err, errNotLoopback) {
		t.Fatalf("got %v", err)
	}

	s = Settings{AutoInterval: "0s", CoreURL: "http://127.0.0.1:8087/"}
	if err := s.check(); err != nil || s.CoreURL != "http://127.0.0.1:8087" {
		t.Fatalf("got %q, %v", s.CoreURL, err)
	}

	s = Settings{AutoInterval: "0s"}
	if err := s.check(); err != nil {
		t.Fatalf("an empty core_url was refused: %v", err)
	}
}

func TestTheCoreClientDoesNotFollowRedirects(t *testing.T) {
	var reached atomic.Int32

	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer elsewhere.Close()

	core := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}))
	defer core.Close()

	d := deleter{base: core.URL, token: "pcl_x", client: coreClient()}

	done, err := d.remove(context.Background(), "local", []string{"camera/a.jpg"})
	if err == nil || done != 0 {
		t.Fatalf("a redirect counted as success: %d, %v", done, err)
	}

	if reached.Load() != 0 {
		t.Fatal("the client followed the redirect")
	}
}

func TestTheCoreClientOnlyDialsLoopback(t *testing.T) {
	client := coreClient()

	if client.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("the client would use a proxy from the environment")
	}

	// 192.0.2.0/24 is reserved for documentation; the dialer refuses before
	// any packet is sent.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://192.0.2.10:9/", nil)

	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatal("a non-loopback address was dialled")
	}

	if !errors.Is(err, errNotLoopback) {
		t.Fatalf("got %v", err)
	}

	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ok.Close()

	resp, err = client.Get(ok.URL)
	if err != nil {
		t.Fatalf("loopback was refused: %v", err)
	}

	resp.Body.Close()
}
