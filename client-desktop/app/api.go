package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/clipcascade/desktop/transfer"
)

func (a *Application) RefreshDeviceCounts() (DeviceCounts, error) {
	if strings.TrimSpace(a.cfg.ServerURL) == "" {
		return DeviceCounts{}, fmt.Errorf("server URL is empty")
	}
	resp, err := a.httpClient.Get(strings.TrimRight(a.cfg.ServerURL, "/") + "/api/devices/online")
	if err != nil {
		return DeviceCounts{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return DeviceCounts{}, fmt.Errorf("device status failed: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	var body struct {
		OnlineDevices int `json:"online_devices"`
		SyncTargets   int `json:"sync_targets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return DeviceCounts{}, err
	}
	a.setDeviceCounts(body.OnlineDevices, body.SyncTargets)
	return DeviceCounts{
		OnlineDevices: body.OnlineDevices,
		SyncTargets:   body.SyncTargets,
		UpdatedAt:     time.Now(),
	}, nil
}

func (a *Application) ListDeferredFiles() ([]transfer.DeferredFileRecord, error) {
	if strings.TrimSpace(a.cfg.ServerURL) == "" {
		return nil, fmt.Errorf("server URL is empty")
	}
	return transfer.ListDeferredFiles(a.httpClient, a.cfg.ServerURL)
}

func (a *Application) RequestDeferredFile(deferredID string) (*transfer.DeferredFileRecord, int, error) {
	if strings.TrimSpace(a.cfg.ServerURL) == "" {
		return nil, 0, fmt.Errorf("server URL is empty")
	}
	return transfer.RequestDeferredFile(a.httpClient, a.cfg.ServerURL, deferredID)
}

func (a *Application) DownloadDeferredFile(item transfer.DeferredFileRecord) (string, error) {
	if strings.TrimSpace(item.RelayID) == "" {
		return "", fmt.Errorf("deferred file is not ready")
	}
	downloads := filepath.Join(configDir(), "downloads")
	if err := os.MkdirAll(downloads, 0o755); err != nil {
		return "", err
	}
	destPath := filepath.Join(downloads, fmt.Sprintf("%d-%s", time.Now().UnixNano(), sanitizeOfferName(item.FileName)))
	transferID := "manual-download-" + item.DeferredID
	a.transferMgr.Start(transferID, transfer.DirectionDownload, item.FileName, item.FileSize)
	if err := transfer.DownloadFile(a.fileHTTPClient, a.cfg.ServerURL, item.RelayID, destPath, item.SHA256, func(done, total int64) {
		a.transferMgr.UpdateProgress(transferID, done, total)
	}); err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		return "", err
	}
	if err := a.clip.PasteLocalFile(destPath); err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		return "", err
	}
	a.transferMgr.MarkCompleted(transferID)
	return destPath, nil
}

func configDir() string {
	return filepath.Dir(newHistoryStore().path)
}
