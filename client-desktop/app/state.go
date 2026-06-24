package app

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/pkg/sizefmt"

	"github.com/clipcascade/desktop/config"
	"github.com/clipcascade/desktop/transfer"
)

const historyLimit = 50

type HistoryItem struct {
	ID          string    `json:"id"`
	Text        string    `json:"text"`
	Type        string    `json:"type"`
	Direction   string    `json:"direction"`
	FileName    string    `json:"file_name,omitempty"`
	ImagePath   string    `json:"image_path,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	DisplaySize string    `json:"display_size,omitempty"`
}

type DeviceCounts struct {
	OnlineDevices int
	SyncTargets   int
	UpdatedAt     time.Time
}

type Snapshot struct {
	Status       string
	StatusText   string
	StatusDetail string
	StatusAt     time.Time

	ServerURL     string
	Username      string
	E2EEEnabled   bool
	P2PEnabled    bool
	AutoReconnect bool
	StunURL       string

	Connected bool
	Devices   DeviceCounts
	LastEvent string
	History   []HistoryItem
	Transfers []transfer.Task
}

type historyStore struct {
	path     string
	imageDir string
}

type appState struct {
	mu           sync.RWMutex
	status       string
	statusText   string
	statusDetail string
	statusAt     time.Time
	devices      DeviceCounts
	lastEvent    string
	history      []HistoryItem
}

func newHistoryStore() *historyStore {
	dir := config.ConfigDir()
	return &historyStore{
		path:     filepath.Join(dir, "history.json"),
		imageDir: filepath.Join(dir, "history-images"),
	}
}

func (s *historyStore) Load() []HistoryItem {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil
	}
	var items []HistoryItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil
	}
	if len(items) > historyLimit {
		items = items[:historyLimit]
	}
	return items
}

func (s *historyStore) Save(items []HistoryItem) {
	if len(items) > historyLimit {
		items = items[:historyLimit]
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(s.path, data, 0o600)
}

func (s *historyStore) SaveImage(payload string) string {
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil || len(data) == 0 {
		return ""
	}
	if err := os.MkdirAll(s.imageDir, 0o755); err != nil {
		return ""
	}
	path := filepath.Join(s.imageDir, fmt.Sprintf("%d.img", time.Now().UnixNano()))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return ""
	}
	return path
}

func newAppState(history []HistoryItem) appState {
	last := "最近事件：暂无同步记录"
	if len(history) > 0 {
		last = formatHistoryEvent(history[0])
	}
	return appState{
		status:       "disconnected",
		statusText:   "Disconnected",
		statusDetail: "未连接",
		statusAt:     time.Now(),
		lastEvent:    last,
		history:      history,
	}
}

func (a *Application) setStatus(status string) {
	a.state.mu.Lock()
	a.state.status = status
	a.state.statusText = status
	a.state.statusDetail = statusDetail(status)
	a.state.statusAt = time.Now()
	a.state.mu.Unlock()

	if a.tray != nil {
		a.tray.SetStatus(status)
	}
}

func (a *Application) setDeviceCounts(online, targets int) {
	a.state.mu.Lock()
	a.state.devices = DeviceCounts{
		OnlineDevices: online,
		SyncTargets:   targets,
		UpdatedAt:     time.Now(),
	}
	a.state.mu.Unlock()
}

func (a *Application) appendHistory(payload string, payloadType string, filename string, direction string) {
	item := a.historyItem(payload, payloadType, filename, direction)
	if item.Text == "" && item.ImagePath == "" && item.FileName == "" {
		return
	}

	a.state.mu.Lock()
	if len(a.state.history) > 0 {
		last := a.state.history[0]
		if last.Type == item.Type && last.Text == item.Text && last.FileName == item.FileName && last.Direction == item.Direction {
			a.state.mu.Unlock()
			return
		}
	}
	a.state.history = append([]HistoryItem{item}, a.state.history...)
	if len(a.state.history) > historyLimit {
		a.state.history = a.state.history[:historyLimit]
	}
	a.state.lastEvent = formatHistoryEvent(item)
	items := append([]HistoryItem(nil), a.state.history...)
	a.state.mu.Unlock()

	a.historyStore.Save(items)
}

func (a *Application) historyItem(payload string, payloadType string, filename string, direction string) HistoryItem {
	now := time.Now()
	item := HistoryItem{
		ID:        fmt.Sprintf("%d", now.UnixNano()),
		Type:      payloadType,
		Direction: direction,
		FileName:  strings.TrimSpace(filename),
		CreatedAt: now,
	}

	switch payloadType {
	case constants.TypeText:
		item.Text = compactText(payload, 500)
	case constants.TypeImage:
		item.Text = "图片"
		item.ImagePath = a.historyStore.SaveImage(payload)
		item.DisplaySize = sizefmt.FormatBytes(int64(sizefmt.EstimatedBase64DecodedSize(payload)))
	case constants.TypeFileEager:
		item.Text = "文件：" + fallbackName(filename, "文件")
		item.DisplaySize = sizefmt.FormatBytes(int64(sizefmt.EstimatedBase64DecodedSize(payload)))
	case constants.TypeFileStub, constants.TypeFiles:
		item.Text = "大文件：" + summarizeFileStub(payload, filename)
	case constants.TypeFileOffer:
		var offer transfer.FileOfferPayload
		if err := json.Unmarshal([]byte(payload), &offer); err == nil {
			item.FileName = offer.FileName
			item.Text = "文件：" + fallbackName(offer.FileName, "文件")
			item.DisplaySize = sizefmt.FormatBytes(offer.FileSize)
		} else {
			item.Text = "文件"
		}
	default:
		item.Text = compactText(payload, 160)
	}
	return item
}

func (a *Application) ClearHistory() {
	a.state.mu.Lock()
	a.state.history = nil
	a.state.lastEvent = "最近事件：暂无同步记录"
	a.state.mu.Unlock()
	a.historyStore.Save(nil)
}

func (a *Application) Snapshot() Snapshot {
	a.state.mu.RLock()
	state := Snapshot{
		Status:       a.state.status,
		StatusText:   a.state.statusText,
		StatusDetail: a.state.statusDetail,
		StatusAt:     a.state.statusAt,
		Devices:      a.state.devices,
		LastEvent:    a.state.lastEvent,
		History:      append([]HistoryItem(nil), a.state.history...),
	}
	a.state.mu.RUnlock()

	state.ServerURL = a.cfg.ServerURL
	state.Username = a.cfg.Username
	state.E2EEEnabled = a.cfg.E2EEEnabled
	state.P2PEnabled = a.cfg.P2PEnabled
	state.AutoReconnect = a.cfg.AutoReconnect
	state.StunURL = a.cfg.StunURL
	state.Connected = a.IsConnected()
	if a.transferMgr != nil {
		state.Transfers = a.transferMgr.Snapshot()
	}
	return state
}

func (a *Application) IsConnected() bool {
	return a.stomp != nil && a.stomp.IsConnected()
}

func statusDetail(status string) string {
	switch status {
	case "Connecting...":
		return "正在连接服务器"
	case "Connected ✓":
		return "实时同步已启动"
	case "Reconnecting...":
		return "连接中断，正在自动重连"
	case "Disconnected":
		return "已断开连接"
	case "Login Failed":
		return "登录失败，请检查服务器地址、账号或密码"
	case "WS Failed":
		return "WebSocket 连接失败"
	case "Key Error":
		return "端到端加密密钥派生失败"
	case "Clipboard Error":
		return "剪贴板初始化失败"
	case "Transfer Failed":
		return "文件传输失败"
	case "Deferred File Ready":
		return "大文件已登记到服务端"
	default:
		if strings.TrimSpace(status) == "" {
			return "未连接"
		}
		return status
	}
}

func formatHistoryEvent(item HistoryItem) string {
	direction := "接收"
	if item.Direction == "sent" {
		direction = "发送"
	}
	kind := "文本"
	switch item.Type {
	case constants.TypeImage:
		kind = "图片"
	case constants.TypeFileEager, constants.TypeFileStub, constants.TypeFiles, constants.TypeFileOffer:
		kind = "文件"
	}
	return fmt.Sprintf("最近事件：%s%s %s", direction, kind, item.CreatedAt.Format("15:04:05"))
}

func compactText(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if limit <= 0 || len([]rune(text)) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}

func fallbackName(name string, fallback string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return fallback
	}
	return name
}

func summarizeFileStub(payload string, meta string) string {
	if strings.TrimSpace(meta) != "" {
		var decoded struct {
			Count      int      `json:"count"`
			TotalBytes int64    `json:"total_bytes"`
			Names      []string `json:"names"`
		}
		if err := json.Unmarshal([]byte(meta), &decoded); err == nil && decoded.Count > 0 {
			if len(decoded.Names) > 0 {
				return decoded.Names[0]
			}
			return fmt.Sprintf("%d 个文件", decoded.Count)
		}
	}
	lines := strings.FieldsFunc(payload, func(r rune) bool { return r == '\n' || r == '\r' })
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			return filepath.Base(line)
		}
	}
	return "文件"
}
