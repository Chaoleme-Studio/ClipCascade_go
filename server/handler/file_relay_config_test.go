package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/server/config"
	"github.com/gofiber/fiber/v2"
)

func TestFilePolicyAndDeferredRegistrationUseConfiguredLimit(t *testing.T) {
	for _, limit := range []int64{constants.DefaultFileRelayMaxBytes, 20 << 30} {
		cfg := &config.Config{FileRelayEnabled: true, FileRelayMaxBytes: limit, FileRelayTTLSeconds: 3600}
		h := NewFileTransferHandler(newTestDB(t), cfg, NewWSHub())
		app := fiber.New()
		app.Get("/api/files/config", NewStatusHandler(cfg).FileRelayConfig)
		app.Post("/api/files/deferred/register", withUser("alice", h.RegisterDeferred))
		resp, err := app.Test(httptest.NewRequest(http.MethodGet, "/api/files/config", nil))
		if err != nil {
			t.Fatal(err)
		}
		var policy struct {
			Enabled bool  `json:"enabled"`
			Max     int64 `json:"max_file_bytes"`
		}
		err = json.NewDecoder(resp.Body).Decode(&policy)
		resp.Body.Close()
		if err != nil || !policy.Enabled || policy.Max != limit {
			t.Fatalf("unexpected policy: %+v, %v", policy, err)
		}
		for _, size := range []int64{limit, limit + 1} {
			body, _ := json.Marshal(map[string]any{"file_name": "movie.mp4", "file_size": size})
			req := httptest.NewRequest(http.MethodPost, "/api/files/deferred/register", strings.NewReader(string(body)))
			req.Header.Set("Content-Type", "application/json")
			resp, err := app.Test(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			want := http.StatusOK
			if size > limit {
				want = http.StatusRequestEntityTooLarge
			}
			if resp.StatusCode != want {
				t.Fatalf("limit=%d size=%d: status=%d, want %d", limit, size, resp.StatusCode, want)
			}
		}
	}
}
