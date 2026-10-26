package transfer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

func ListDeferredFiles(client *http.Client, serverURL string) ([]DeferredFileRecord, error) {
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(serverURL, "/")+"/api/files/deferred", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("list deferred files failed: %s", string(data))
	}
	var records []DeferredFileRecord
	if err := json.NewDecoder(resp.Body).Decode(&records); err != nil {
		return nil, err
	}
	return records, nil
}

func RegisterDeferredFile(client *http.Client, serverURL string, reqBody *DeferredRegisterRequest) (*DeferredFileRecord, error) {
	if reqBody == nil {
		return nil, fmt.Errorf("register request is nil")
	}
	policy, err := FetchFileRelayConfig(client, serverURL)
	if err != nil {
		return nil, err
	}
	if err := policy.CheckSize(reqBody.FileSize); err != nil {
		return nil, err
	}
	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, strings.TrimRight(serverURL, "/")+"/api/files/deferred/register", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("register deferred file failed: %s", string(data))
	}
	var record DeferredFileRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, err
	}
	return &record, nil
}

func RequestDeferredFile(client *http.Client, serverURL string, deferredID string) (*DeferredFileRecord, int, error) {
	url := strings.TrimRight(serverURL, "/") + "/api/files/deferred/" + strings.TrimSpace(deferredID) + "/request"
	req, err := http.NewRequest(http.MethodPost, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, 0, fmt.Errorf("request deferred file failed: %s", string(data))
	}
	var body struct {
		Item          DeferredFileRecord `json:"item"`
		ActiveClients int                `json:"active_clients"`
		DeferredFileRecord
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, 0, err
	}
	record := body.Item
	if record.DeferredID == "" {
		record = body.DeferredFileRecord
	}
	return &record, body.ActiveClients, nil
}

func MarkDeferredFileReady(client *http.Client, serverURL string, deferredID string, relayID string) (*DeferredFileRecord, error) {
	return postDeferredStatus(client, serverURL, deferredID, "/complete", map[string]string{"relay_id": relayID})
}

func MarkDeferredFileUploading(client *http.Client, serverURL string, deferredID string, uploadedBytes int64) (*DeferredFileRecord, error) {
	return postDeferredStatus(client, serverURL, deferredID, "/progress", map[string]int64{"uploaded_bytes": uploadedBytes})
}

func MarkDeferredFileFailed(client *http.Client, serverURL string, deferredID string, errMsg string) (*DeferredFileRecord, error) {
	return postDeferredStatus(client, serverURL, deferredID, "/fail", map[string]string{"error": errMsg})
}

func postDeferredStatus(client *http.Client, serverURL string, deferredID string, suffix string, payload any) (*DeferredFileRecord, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(serverURL, "/") + "/api/files/deferred/" + deferredID + suffix
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("update deferred file failed: %s", string(data))
	}
	var record DeferredFileRecord
	if err := json.NewDecoder(resp.Body).Decode(&record); err != nil {
		return nil, err
	}
	return &record, nil
}
