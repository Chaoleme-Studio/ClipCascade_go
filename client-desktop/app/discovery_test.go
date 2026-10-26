package app

import (
	"context"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/clipcascade/desktop/config"
	"github.com/grandcat/zeroconf"
)

func TestDiscoveryReturnsSoonAfterFirstResult(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	entries := make(chan *zeroconf.ServiceEntry, 2)
	entry := &zeroconf.ServiceEntry{AddrIPv4: []net.IP{net.ParseIP("192.168.56.12"), net.ParseIP("198.18.0.1")}}
	entry.Port = 8080
	entries <- entry
	entries <- entry
	start := time.Now()
	urls, err := collectDiscoveredServers(ctx, entries, 10*time.Millisecond)
	if err != nil || len(urls) != 1 || urls[0] != "http://192.168.56.12:8080" {
		t.Fatalf("unexpected discovery: %v, %v", urls, err)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("discovery waited for full timeout despite having a result")
	}
}

func TestCandidateLoginDoesNotWaitForDeadAddress(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer slow.Close()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-started
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "winning-session", Path: "/"})
		http.Redirect(w, r, "/", http.StatusFound)
	}))
	defer fast.Close()
	jar, _ := cookiejar.New(nil)
	a := &Application{cfg: &config.Config{Username: "alice", Password: "secret"}, httpClient: &http.Client{Jar: jar}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	winner, cookies, err := a.raceServers(ctx, []string{slow.URL}, func(_ context.Context, found func(string)) {
		found(slow.URL)
		found(fast.URL)
	})
	if err != nil || winner != fast.URL || len(cookies) != 1 || cookies[0].Value != "winning-session" {
		t.Fatalf("unexpected winner: %q, %v, %v", winner, cookies, err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("healthy candidate was blocked by the unresponsive address")
	}
	u, _ := url.Parse(winner)
	if got := jar.Cookies(u); len(got) != 1 || got[0].Value != "winning-session" {
		t.Fatalf("winning cookies not copied for subsequent requests: %v", got)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("losing login request was not canceled")
	}
}
