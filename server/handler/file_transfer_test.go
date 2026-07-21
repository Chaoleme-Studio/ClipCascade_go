package handler

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/server/config"
	"github.com/clipcascade/server/model"
	"github.com/gofiber/fiber/v2"
	"gorm.io/gorm"
)

func TestUploadRejectsOversizedFile(t *testing.T) {
	db := newTestDB(t)
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        t.TempDir(),
		FileRelayMaxBytes:   4,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	app := fiber.New(fiber.Config{StreamRequestBody: true})
	app.Post("/api/files/upload", withUser("alice", h.Upload))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "bad.apk")
	if err != nil {
		t.Fatalf("create multipart part failed: %v", err)
	}
	if _, err := part.Write([]byte("12345")); err != nil {
		t.Fatalf("write multipart part failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/files/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-File-Size", "5")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("upload request should return an HTTP error response, got app error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected oversized upload to return 413, got %d", resp.StatusCode)
	}

	var count int64
	if err := db.Model(&model.FileTransfer{}).Count(&count).Error; err != nil {
		t.Fatalf("count file transfers failed: %v", err)
	}
	if count != 0 {
		t.Fatalf("oversized upload should not create relay metadata, got %d records", count)
	}
}

func TestUploadStoresRelayFile(t *testing.T) {
	db := newTestDB(t)
	tempDir := t.TempDir()
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        tempDir,
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	app := fiber.New()
	app.Post("/api/files/upload", withUser("alice", h.Upload))

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "small.apk")
	if err != nil {
		t.Fatalf("create multipart part failed: %v", err)
	}
	content := []byte("apk payload")
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write multipart part failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer failed: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/files/upload", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-File-Size", "11")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected upload status: %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode upload response failed: %v", err)
	}
	relayID, _ := payload["relay_id"].(string)
	if relayID == "" {
		t.Fatalf("expected relay_id in response")
	}

	var record model.FileTransfer
	if err := db.Where("relay_id = ? AND owner_username = ?", relayID, "alice").First(&record).Error; err != nil {
		t.Fatalf("expected relay metadata, got err=%v", err)
	}
	if record.FileName != "small.apk" || record.FileSize != int64(len(content)) {
		t.Fatalf("unexpected relay metadata: name=%q size=%d", record.FileName, record.FileSize)
	}
	expectedSHA := sha256Hex(content)
	if record.SHA256 != expectedSHA {
		t.Fatalf("unexpected sha256: %s", record.SHA256)
	}
	stored, err := os.ReadFile(record.StoragePath)
	if err != nil {
		t.Fatalf("read stored relay file failed: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Fatalf("stored relay content mismatch")
	}
}

func TestUploadStoresChunkedStreamRelayFile(t *testing.T) {
	db := newTestDB(t)
	tempDir := t.TempDir()
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        tempDir,
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	app := fiber.New(fiber.Config{
		StreamRequestBody:            true,
		DisablePreParseMultipartForm: true,
		DisableStartupMessage:        true,
	})
	app.Post("/api/files/upload", withUser("alice", h.Upload))

	content := bytes.Repeat([]byte("chunked-stream-payload-"), 1024)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	serveErrCh := make(chan error, 1)
	go func() {
		serveErrCh <- app.Listener(ln)
	}()
	t.Cleanup(func() {
		_ = app.Shutdown()
		if err := <-serveErrCh; err != nil {
			t.Errorf("listener failed: %v", err)
		}
	})

	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	errCh := make(chan error, 1)
	go func() {
		defer close(errCh)
		part, err := writer.CreateFormFile("file", "stream.bin")
		if err == nil {
			_, err = part.Write(content)
		}
		if closeErr := writer.Close(); err == nil {
			err = closeErr
		}
		if closeErr := pw.Close(); err == nil {
			err = closeErr
		}
		errCh <- err
	}()

	req, err := http.NewRequest(http.MethodPost, "http://"+ln.Addr().String()+"/api/files/upload", pr)
	if err != nil {
		t.Fatalf("create upload request failed: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-File-Size", fmt.Sprintf("%d", len(content)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload request failed: %v", err)
	}
	defer resp.Body.Close()
	if writeErr := <-errCh; writeErr != nil {
		t.Fatalf("write multipart stream failed: %v", writeErr)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected upload status: %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode upload response failed: %v", err)
	}
	relayID, _ := payload["relay_id"].(string)
	if relayID == "" {
		t.Fatalf("expected relay_id in response")
	}

	var record model.FileTransfer
	if err := db.Where("relay_id = ? AND owner_username = ?", relayID, "alice").First(&record).Error; err != nil {
		t.Fatalf("expected relay metadata, got err=%v", err)
	}
	if record.FileName != "stream.bin" || record.FileSize != int64(len(content)) {
		t.Fatalf("unexpected relay metadata: name=%q size=%d", record.FileName, record.FileSize)
	}
	if record.SHA256 != sha256Hex(content) {
		t.Fatalf("unexpected sha256: %s", record.SHA256)
	}
	stored, err := os.ReadFile(record.StoragePath)
	if err != nil {
		t.Fatalf("read stored relay file failed: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Fatalf("stored relay content mismatch")
	}
}

func TestRegisterDeferredFileAndList(t *testing.T) {
	db := newTestDB(t)
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        t.TempDir(),
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	app := fiber.New()
	app.Post("/api/files/deferred/register", withUser("alice", h.RegisterDeferred))
	app.Get("/api/files/deferred", withUser("alice", h.ListDeferred))

	body, _ := json.Marshal(map[string]any{
		"file_name": "movie.mkv",
		"file_size": constants.DefaultFileAutoRelayMaxBytes + 1,
		"mime_type": "video/x-matroska",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/files/deferred/register", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("register request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected register status: %d", resp.StatusCode)
	}

	var created map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode register response failed: %v", err)
	}
	if created["status"] != model.DeferredFileStatusPending {
		t.Fatalf("unexpected register status payload: %v", created["status"])
	}
	if created["deferred_id"] == "" {
		t.Fatalf("expected deferred_id in response")
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/files/deferred", nil)
	listResp, err := app.Test(listReq, -1)
	if err != nil {
		t.Fatalf("list request failed: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected list status: %d", listResp.StatusCode)
	}

	var items []map[string]any
	if err := json.NewDecoder(listResp.Body).Decode(&items); err != nil {
		t.Fatalf("decode list response failed: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 deferred item, got %d", len(items))
	}
	if items[0]["file_name"] != "movie.mkv" {
		t.Fatalf("unexpected file name: %v", items[0]["file_name"])
	}
}

func TestProgressAndCompleteDeferredFile(t *testing.T) {
	db := newTestDB(t)
	tempDir := t.TempDir()
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        tempDir,
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	record := &model.DeferredFile{
		DeferredID:    "deferred-1",
		OwnerUsername: "alice",
		FileName:      "archive.zip",
		FileSize:      constants.DefaultFileAutoRelayMaxBytes + 1024,
		MimeType:      "application/zip",
		Status:        model.DeferredFileStatusPending,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("create deferred record failed: %v", err)
	}

	filePath := filepath.Join(tempDir, "relay-file.zip")
	if err := os.WriteFile(filePath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("write relay file failed: %v", err)
	}
	relay := &model.FileTransfer{
		RelayID:       "relay-1",
		OwnerUsername: "alice",
		FileName:      "archive.zip",
		FileSize:      5,
		SHA256:        "abc123",
		MimeType:      "application/zip",
		StoragePath:   filePath,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(relay).Error; err != nil {
		t.Fatalf("create relay record failed: %v", err)
	}

	app := fiber.New()
	app.Post("/api/files/deferred/:deferredID/progress", withUser("alice", h.ProgressDeferred))
	app.Post("/api/files/deferred/:deferredID/complete", withUser("alice", h.CompleteDeferred))
	app.Get("/api/files/deferred/:deferredID", withUser("alice", h.GetDeferred))

	progressBody, _ := json.Marshal(map[string]int64{"uploaded_bytes": 3})
	progressReq := httptest.NewRequest(http.MethodPost, "/api/files/deferred/deferred-1/progress", bytes.NewReader(progressBody))
	progressReq.Header.Set("Content-Type", "application/json")
	progressResp, err := app.Test(progressReq, -1)
	if err != nil {
		t.Fatalf("progress request failed: %v", err)
	}
	defer progressResp.Body.Close()
	if progressResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected progress status: %d", progressResp.StatusCode)
	}

	var progressPayload map[string]any
	if err := json.NewDecoder(progressResp.Body).Decode(&progressPayload); err != nil {
		t.Fatalf("decode progress payload failed: %v", err)
	}
	if progressPayload["status"] != model.DeferredFileStatusUploading {
		t.Fatalf("unexpected progress status payload: %v", progressPayload["status"])
	}
	if progressPayload["upload_percent"] != float64(0) {
		t.Fatalf("unexpected upload percent for partial upload: %v", progressPayload["upload_percent"])
	}
	if progressPayload["upload_speed_human"] == "" {
		t.Fatalf("expected upload speed to be exposed during upload")
	}

	completeBody, _ := json.Marshal(map[string]string{"relay_id": "relay-1"})
	completeReq := httptest.NewRequest(http.MethodPost, "/api/files/deferred/deferred-1/complete", bytes.NewReader(completeBody))
	completeReq.Header.Set("Content-Type", "application/json")
	completeResp, err := app.Test(completeReq, -1)
	if err != nil {
		t.Fatalf("complete request failed: %v", err)
	}
	defer completeResp.Body.Close()
	if completeResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected complete status: %d", completeResp.StatusCode)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/files/deferred/deferred-1", nil)
	getResp, err := app.Test(getReq, -1)
	if err != nil {
		t.Fatalf("get request failed: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected get status: %d", getResp.StatusCode)
	}

	var finalRecord map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&finalRecord); err != nil {
		t.Fatalf("decode final record failed: %v", err)
	}
	if finalRecord["status"] != model.DeferredFileStatusReady {
		t.Fatalf("unexpected final status: %v", finalRecord["status"])
	}
	if finalRecord["download_url"] != "/api/files/relay-1" {
		t.Fatalf("unexpected download url: %v", finalRecord["download_url"])
	}
	if finalRecord["upload_percent"] != float64(100) {
		t.Fatalf("unexpected final upload percent: %v", finalRecord["upload_percent"])
	}
}

func TestDownloadRelayFileRequiresOwner(t *testing.T) {
	db := newTestDB(t)
	tempDir := t.TempDir()
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        tempDir,
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	filePath := filepath.Join(tempDir, "private.txt")
	content := []byte("alice private file")
	if err := os.WriteFile(filePath, content, 0o644); err != nil {
		t.Fatalf("write relay file failed: %v", err)
	}
	relay := &model.FileTransfer{
		RelayID:       "relay-private",
		OwnerUsername: "alice",
		FileName:      "private.txt",
		FileSize:      int64(len(content)),
		SHA256:        "abc123",
		MimeType:      "text/plain",
		StoragePath:   filePath,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(relay).Error; err != nil {
		t.Fatalf("create relay record failed: %v", err)
	}

	app := fiber.New()
	app.Get("/api/files/:relayID", withUser("bob", h.Download))

	req := httptest.NewRequest(http.MethodGet, "/api/files/relay-private", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("download request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected non-owner download to be hidden as 404, got %d", resp.StatusCode)
	}
}

func TestRequestDeferredWithoutOnlineSourceReturnsPendingMessage(t *testing.T) {
	db := newTestDB(t)
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        t.TempDir(),
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	record := &model.DeferredFile{
		DeferredID:    "deferred-offline",
		OwnerUsername: "alice",
		FileName:      "offline.bin",
		FileSize:      constants.DefaultFileAutoRelayMaxBytes + 1024,
		MimeType:      "application/octet-stream",
		Status:        model.DeferredFileStatusPending,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("create deferred record failed: %v", err)
	}

	app := fiber.New()
	app.Post("/api/files/deferred/:deferredID/request", withUser("alice", h.RequestDeferred))

	req := httptest.NewRequest(http.MethodPost, "/api/files/deferred/deferred-offline/request", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("request download failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected request status: %d", resp.StatusCode)
	}

	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode payload failed: %v", err)
	}
	if payload["active_clients"] != float64(0) {
		t.Fatalf("expected active_clients=0, got %v", payload["active_clients"])
	}
	item, ok := payload["item"].(map[string]any)
	if !ok {
		t.Fatalf("expected item payload")
	}
	if item["status"] != model.DeferredFileStatusPending {
		t.Fatalf("unexpected item status: %v", item["status"])
	}
	if item["last_error"] == "" {
		t.Fatalf("expected last_error when source client is offline")
	}
}

func TestDeleteDeferredFileRemovesRelayAndRecord(t *testing.T) {
	db := newTestDB(t)
	tempDir := t.TempDir()
	cfg := &config.Config{
		FileRelayEnabled:    true,
		FileRelayDir:        tempDir,
		FileRelayMaxBytes:   constants.DefaultFileRelayMaxBytes,
		FileRelayTTLSeconds: 3600,
	}
	h := NewFileTransferHandler(db, cfg, NewWSHub())

	relayPath := filepath.Join(tempDir, "relay-delete.bin")
	if err := os.WriteFile(relayPath, []byte("delete-me"), 0o644); err != nil {
		t.Fatalf("write relay file failed: %v", err)
	}
	relay := &model.FileTransfer{
		RelayID:       "relay-delete",
		OwnerUsername: "alice",
		FileName:      "delete.bin",
		FileSize:      int64(len("delete-me")),
		SHA256:        "deadbeef",
		MimeType:      "application/octet-stream",
		StoragePath:   relayPath,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(relay).Error; err != nil {
		t.Fatalf("create relay record failed: %v", err)
	}
	record := &model.DeferredFile{
		DeferredID:    "deferred-delete",
		OwnerUsername: "alice",
		FileName:      "delete.bin",
		FileSize:      relay.FileSize,
		MimeType:      relay.MimeType,
		Status:        model.DeferredFileStatusReady,
		RelayID:       relay.RelayID,
		SHA256:        relay.SHA256,
		ExpiresAt:     time.Now().Add(time.Hour),
	}
	if err := db.Create(record).Error; err != nil {
		t.Fatalf("create deferred record failed: %v", err)
	}

	app := fiber.New()
	app.Delete("/api/files/deferred/:deferredID", withUser("alice", h.DeleteDeferred))

	req := httptest.NewRequest(http.MethodDelete, "/api/files/deferred/deferred-delete", nil)
	resp, err := app.Test(req, -1)
	if err != nil {
		t.Fatalf("delete request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected delete status: %d", resp.StatusCode)
	}

	if _, err := os.Stat(relayPath); !os.IsNotExist(err) {
		t.Fatalf("expected relay file to be removed, stat err=%v", err)
	}
	if err := db.Where("deferred_id = ?", "deferred-delete").First(&model.DeferredFile{}).Error; err != gorm.ErrRecordNotFound {
		t.Fatalf("expected deferred record deleted, got err=%v", err)
	}
	if err := db.Where("relay_id = ?", "relay-delete").First(&model.FileTransfer{}).Error; err != gorm.ErrRecordNotFound {
		t.Fatalf("expected relay record deleted, got err=%v", err)
	}
}

func withUser(username string, next fiber.Handler) fiber.Handler {
	return func(c *fiber.Ctx) error {
		c.Locals("username", username)
		return next(c)
	}
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
