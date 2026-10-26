package transfer

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/pkg/sizefmt"
)

type FileRelayConfig struct {
	Enabled      bool  `json:"enabled"`
	MaxFileBytes int64 `json:"max_file_bytes"`
}

// FetchFileRelayConfig refreshes per transfer, so changing servers or restarting
// a server with a new limit cannot leave a stale client-side limit behind.
func FetchFileRelayConfig(client *http.Client, serverURL string) (FileRelayConfig, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(serverURL, "/")+"/api/files/config", nil)
	if err != nil {
		return FileRelayConfig{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return FileRelayConfig{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// Servers before v2.6 do not advertise the limit.
		return FileRelayConfig{Enabled: true, MaxFileBytes: constants.DefaultFileRelayMaxBytes}, nil
	}
	if resp.StatusCode != http.StatusOK {
		return FileRelayConfig{}, fmt.Errorf("file relay config failed: HTTP %d", resp.StatusCode)
	}
	var policy FileRelayConfig
	if err := json.NewDecoder(resp.Body).Decode(&policy); err != nil {
		return FileRelayConfig{}, err
	}
	if policy.MaxFileBytes <= 0 {
		return FileRelayConfig{}, fmt.Errorf("server returned an invalid file limit")
	}
	return policy, nil
}

func (c FileRelayConfig) CheckSize(size int64) error {
	if !c.Enabled {
		return fmt.Errorf("server file relay is disabled")
	}
	if size <= 0 || size > c.MaxFileBytes {
		return fmt.Errorf("file size exceeds server limit (%s)", sizefmt.FormatBytes(c.MaxFileBytes))
	}
	return nil
}
