package transfer

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestDeferredRegistrationRefreshesServerLimit(t *testing.T) {
	var limit atomic.Int64
	limit.Store(20 << 30)
	var registrations atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/files/config" {
			fmt.Fprintf(w, `{"enabled":true,"max_file_bytes":%d}`, limit.Load())
			return
		}
		if r.URL.Path != "/api/files/deferred/register" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		registrations.Add(1)
		var req DeferredRegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
		}
		json.NewEncoder(w).Encode(DeferredFileRecord{DeferredID: "movie", FileSize: req.FileSize})
	}))
	defer server.Close()
	request := &DeferredRegisterRequest{FileName: "movie.mp4", FileSize: 3 << 30}
	if _, err := RegisterDeferredFile(server.Client(), server.URL, request); err != nil {
		t.Fatal(err)
	}
	limit.Store(2 << 30)
	if _, err := RegisterDeferredFile(server.Client(), server.URL, request); err == nil {
		t.Fatal("lowered limit must reject the same file")
	}
	if registrations.Load() != 1 {
		t.Fatal("oversized file reached registration endpoint")
	}
}

func TestFilePolicyLegacyFallbackAndErrors(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusUnauthorized, http.StatusInternalServerError} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) }))
			defer server.Close()
			policy, err := FetchFileRelayConfig(server.Client(), server.URL)
			if status == http.StatusNotFound {
				if err != nil || !policy.Enabled || policy.MaxFileBytes != 2<<30 {
					t.Fatalf("unexpected fallback: %+v, %v", policy, err)
				}
			} else if err == nil {
				t.Fatal("must not hide authentication or server errors")
			}
		})
	}
	for _, body := range []string{`{}`, `{"enabled":true,"max_file_bytes":-1}`, `not json`} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
		_, err := FetchFileRelayConfig(server.Client(), server.URL)
		server.Close()
		if err == nil {
			t.Fatalf("accepted invalid policy %s", body)
		}
	}
}

func TestUploadChecksConfiguredLimitBeforeSendingBody(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{true, false} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/files/config" {
				t.Errorf("must reject before upload: %s", r.URL.Path)
			}
			fmt.Fprintf(w, `{"enabled":%t,"max_file_bytes":4}`, enabled)
		}))
		_, err := UploadFile(server.Client(), server.URL, path, nil)
		server.Close()
		if err == nil {
			t.Fatal("expected early rejection")
		}
	}
}

func TestUploadAtConfiguredLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.bin")
	if err := os.WriteFile(path, []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	var uploaded atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/files/config" {
			fmt.Fprint(w, `{"enabled":true,"max_file_bytes":5}`)
			return
		}
		if r.URL.Path != "/api/files/upload" || r.Header.Get("X-File-Size") != "5" {
			t.Errorf("unexpected upload: %s, size=%s", r.URL.Path, r.Header.Get("X-File-Size"))
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		uploaded.Store(true)
		fmt.Fprint(w, `{"relay_id":"accepted","file_size":5}`)
	}))
	defer server.Close()
	result, err := UploadFile(server.Client(), server.URL, path, nil)
	if err != nil || !uploaded.Load() || result.RelayID != "accepted" {
		t.Fatalf("upload at exact limit failed: result=%+v, err=%v", result, err)
	}
}
