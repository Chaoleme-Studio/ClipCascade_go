// Package app 管理 desktop client 生命周期：login、connect、监控、reconnect。
package app

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/clipcascade/pkg/constants"
	pkgcrypto "github.com/clipcascade/pkg/crypto"
	"github.com/clipcascade/pkg/protocol"
	"github.com/clipcascade/pkg/sizefmt"

	"github.com/clipcascade/desktop/clipboard"
	"github.com/clipcascade/desktop/config"
	"github.com/clipcascade/desktop/transfer"
	"github.com/clipcascade/desktop/transport"
	"github.com/google/uuid"
)

type trayController interface {
	OnConnect(func())
	OnDisconnect(func())
	OnQuit(func())
	Run()
	SetStatus(string)
}

// Application 是主要的 desktop client 控制器。
type Application struct {
	cfg               *config.Config
	httpClient        *http.Client // 通用 HTTP 客户端，15s 超时（登录/认证）
	fileHTTPClient    *http.Client // 文件传输专用，无超时（大文件上传/下载）
	stomp             *transport.StompClient
	p2p               *transport.P2PClient
	sessionCookies    []*http.Cookie // 会话 cookie，用于 P2P 断线自动重连
	clip              *clipboard.Manager
	tray              trayController
	transferMgr       *transfer.Manager
	historyStore      *historyStore
	state             appState
	ctx               context.Context
	cancel            context.CancelFunc
	encKey            []byte // 从 password 派生的 AES-256-GCM 密钥
	reconnecting      bool
	connecting        bool // 防止用户重复点击连接产生并发泄漏
	connectionDesired bool
	connectCancel     context.CancelFunc
	started           bool
	connMu            sync.Mutex

	lastRecvMu   sync.Mutex
	lastRecvHash string
	lastRecvTime time.Time
	recvUUIDs    map[string]time.Time // MessageUUID 去重窗口（跨通道/重连重复投递）

	pastMu            sync.Mutex
	lastPastedPayload string // 最近一次粘贴到本机的内容，用于回环守卫
	lastPastedTime    time.Time

	pendingMu       sync.Mutex
	pendingSends    []pendingSend // 发送失败待重发队列（重连成功后 flush）
	pendingFlushing bool

	deferredMu    sync.Mutex
	deferredFiles map[string]string
}

// pendingSend 是一条等待重发的剪贴板消息。
type pendingSend struct {
	body        string // 已编码（可能已加密）的完整消息体
	payload     string
	payloadType string
	filename    string
}

// New 创建一个新的 Application 实例。
func New(cfg *config.Config) *Application {
	return newApplication(cfg, true)
}

// NewWindowApplication 创建一个不接管 systray 的 Application，供 Fyne 桌面窗口使用。
func NewWindowApplication(cfg *config.Config) *Application {
	return newApplication(cfg, false)
}

func newApplication(cfg *config.Config, withTray bool) *Application {
	ctx, cancel := context.WithCancel(context.Background())
	jar, _ := cookiejar.New(nil)
	store := newHistoryStore()
	var tray trayController
	if withTray {
		tray = newTrayController()
	}

	app := &Application{
		cfg:           cfg,
		clip:          clipboard.NewManager(),
		tray:          tray,
		historyStore:  store,
		state:         newAppState(store.Load()),
		ctx:           ctx,
		cancel:        cancel,
		deferredFiles: make(map[string]string),
		recvUUIDs:     make(map[string]time.Time),
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse // 不要跟随重定向
			},
		},
	}
	// 文件传输专用客户端：共享 cookie jar，无总超时（大文件可能耗时数分钟），
	// 但响应头必须在 30s 内到达，避免服务端挂起时 goroutine 永久阻塞。
	transportBase := http.DefaultTransport.(*http.Transport).Clone()
	transportBase.ResponseHeaderTimeout = 30 * time.Second
	app.fileHTTPClient = &http.Client{
		Jar:       jar,
		Timeout:   0,
		Transport: transportBase,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	app.loadDeferredFiles()
	app.transferMgr = transfer.NewManager(app.setStatus, notify)
	app.clip.SetNotifier(notify)
	return app
}

// Run 启动 application。在 macOS 上必须从 main goroutine 调用。
func (a *Application) Run() {
	if err := a.Start(true); err != nil {
		slog.Error("application start failed", "error", err)
		a.setStatus("Clipboard Error")
		return
	}

	if a.tray == nil {
		return
	}

	// 设置 tray 回调
	a.tray.OnConnect(func() {
		go a.Connect()
	})
	a.tray.OnDisconnect(func() {
		a.disconnect()
	})
	a.tray.OnQuit(func() {
		a.shutdown()
	})

	// Run tray (blocks until quit)
	a.tray.Run()
}

func (a *Application) Start(autoConnect bool) error {
	a.connMu.Lock()
	if a.started {
		a.connMu.Unlock()
		return nil
	}
	a.started = true
	a.connMu.Unlock()

	// 启动即清理 24 小时前的接收临时文件与历史图片，避免长期累积。
	a.clip.CleanupExpiredTempFiles()
	go a.historyStore.CleanupImages(24 * time.Hour)

	if err := a.clip.Init(); err != nil {
		a.setStatus("Clipboard Error")
		return fmt.Errorf("clipboard init failed: %w", err)
	}
	a.clip.OnCopy(a.onCopy)
	a.clip.Watch(a.ctx)

	go a.monitorConnection()

	// 如果配置了凭据，则自动连接
	if autoConnect && a.cfg.Username != "" && a.cfg.Password != "" {
		go a.Connect()
	}
	return nil
}

func (a *Application) discoverServer() ([]string, error) {
	return discoverServers(a.ctx)
}

func (a *Application) DiscoverServers() ([]string, error) {
	return a.discoverServer()
}

func (a *Application) SaveConfig(serverURL, username, password string, e2eeEnabled, p2pEnabled, autoReconnect bool, stunURL string) error {
	a.cfg.ServerURL = config.NormalizeServerURL(serverURL)
	a.cfg.Username = strings.TrimSpace(username)
	a.cfg.Password = password
	a.cfg.E2EEEnabled = e2eeEnabled
	a.cfg.P2PEnabled = p2pEnabled
	a.cfg.AutoReconnect = autoReconnect
	a.cfg.StunURL = strings.TrimSpace(stunURL)
	if a.cfg.StunURL == "" {
		a.cfg.StunURL = constants.DefaultStunURL
	}
	return a.cfg.Save()
}

func (a *Application) Connect() {
	a.connMu.Lock()
	a.connectionDesired = true
	a.connMu.Unlock()
	a.connect()
}

func (a *Application) Disconnect() {
	a.disconnect()
}

func (a *Application) Shutdown() {
	a.shutdown()
}

// connect 执行 login → 获取加密密钥 → 启动 WebSocket → 开始剪贴板监控。
func (a *Application) connect() {
	a.connMu.Lock()
	if !a.connectionDesired {
		a.connMu.Unlock()
		return
	}
	if a.connecting || (a.stomp != nil && a.stomp.IsConnected()) {
		a.connMu.Unlock()
		slog.Info("应用：已在连接中或已连接，忽略重复请求")
		return
	}
	a.connecting = true
	attemptCtx, cancelAttempt := context.WithCancel(a.ctx)
	a.connectCancel = cancelAttempt
	a.connMu.Unlock()

	retryNeeded := false
	defer func() {
		cancelAttempt()
		a.connMu.Lock()
		a.connecting = false
		a.connectCancel = nil
		shouldRetry := retryNeeded && a.connectionDesired
		a.connMu.Unlock()
		if shouldRetry {
			go a.reconnectLoop()
		}
	}()

	a.setStatus("Connecting...")

	successfulURL, cookies, lastErr := a.findServer(attemptCtx)
	if attemptCtx.Err() != nil {
		return
	}

	if successfulURL == "" {
		retryNeeded = true
		slog.Error("登录完全失败，请检查服务器状态或配置", "错误", lastErr)
		a.setStatus("Login Failed")
		notify("ClipCascade", "Failed to connect to any server")
		return
	}

	successfulURL = config.NormalizeServerURL(successfulURL)
	slog.Info("应用：服务器连接成功", "最终URL", successfulURL)
	a.cfg.ServerURL = successfulURL
	if err := a.cfg.SaveServerURLOnly(successfulURL); err != nil {
		slog.Warn("应用：保存最近可用服务器地址失败", "错误", err)
	}

	// 步骤 2: 获取用于加密密钥派生的 user 信息技巧。
	if a.cfg.E2EEEnabled {
		if err := a.deriveEncryptionKey(); err != nil {
			retryNeeded = true
			slog.Error("密钥派生失败", "错误", err)
			a.setStatus("Key Error")
			return
		}
	}

	// Step 3: Connect WebSocket STOMP
	if attemptCtx.Err() != nil {
		return
	}
	stompClient := transport.NewStompClient(a.cfg.ServerURL, cookies)
	stompClient.OnMessage(a.onReceive)
	a.connMu.Lock()
	a.stomp = stompClient
	a.sessionCookies = cookies
	a.connMu.Unlock()

	if err := stompClient.ConnectContext(attemptCtx); err != nil {
		if attemptCtx.Err() != nil {
			return
		}
		slog.Error("WebSocket 连接失败", "错误", err)
		a.setStatus("WS Failed")
		notify("ClipCascade", "WebSocket connection failed")
		retryNeeded = true
		return
	}

	// Step 4: Connect P2P if enabled
	if a.cfg.P2PEnabled {
		stunURL := constants.DefaultStunURL
		if a.cfg.StunURL != "" {
			stunURL = a.cfg.StunURL
		}
		p2pClient := transport.NewP2PClient(a.cfg.ServerURL, cookies, stunURL)
		p2pClient.OnReceive(a.onReceive)
		a.connMu.Lock()
		a.p2p = p2pClient
		a.connMu.Unlock()
		if err := p2pClient.Connect(); err != nil {
			slog.Warn("应用：P2P 连接失败", "错误", err)
		}
	}

	if attemptCtx.Err() != nil {
		return
	}
	a.setStatus("Connected ✓")
	notify("ClipCascade", "Connected to server as "+a.cfg.Username)
	slog.Info("应用：已连接并开始监控剪贴板")
	go func() {
		_, _ = a.RefreshDeviceCounts()
	}()
	// 重连成功后补发断线期间积压的剪贴板消息
	go a.flushPendingSends()
}

// deriveEncryptionKey 从 server 获取 user 信息并在本地派生 AES 密钥。
func (a *Application) deriveEncryptionKey() error {
	resp, err := a.httpClient.Get(a.cfg.ServerURL + "/api/user-info")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var info struct {
		Salt       string `json:"salt"`
		HashRounds int    `json:"hash_rounds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return err
	}

	a.encKey = pkgcrypto.DeriveKey(a.cfg.Password, a.cfg.Username, info.Salt, info.HashRounds)
	slog.Info("应用：加密密钥已派生", "循环次数", info.HashRounds)
	return nil
}

// onCopy 在本地剪贴板更改时被调用。发送到 server。
func (a *Application) onCopy(payload string, payloadType string, filename string) {
	stomp := a.currentStomp()
	connected := stomp != nil && stomp.IsConnected()
	// 回环守卫：最近粘贴到本机的内容触发的复制事件在 5s 内忽略
	if a.suppressEcho(payload) {
		slog.Debug("应用：忽略粘贴回环触发的复制事件")
		return
	}
	if !a.allowSendType(payloadType) {
		slog.Debug("应用：按发送过滤规则跳过", "类型", payloadType, "文件名", filename)
		return
	}
	if !connected {
		// 文件路径必须在连接恢复后重新登记或上传，不能把本机路径当作普通消息补发。
		if payloadType == constants.TypeFileStub {
			slog.Warn("应用：断线期间无法同步文件，请重连后重新复制")
			return
		}
		// 暂存原始数据；重连派生密钥后再编码，避免 E2EE 尚未就绪时发送明文。
		a.enqueuePendingSend(pendingSend{payload: payload, payloadType: payloadType, filename: filename})
		return
	}
	slog.Info("应用：准备发送剪贴板更新", "类型", payloadType, "大小", sizefmt.HumanSizeFromPayload(payloadType, payload))
	if payloadType == constants.TypeFileStub && a.tryRelayLargeFile(payload) {
		return
	}
	if payloadType == constants.TypeFileStub && a.tryRegisterDeferredFile(payload) {
		return
	}

	clipData := &protocol.ClipboardData{
		Payload:     payload,
		Type:        payloadType,
		FileName:    filename,
		MessageUUID: uuid.New().String(),
		DeviceID:    a.cfg.DeviceID,
		TimestampMs: time.Now().UnixMilli(),
	}
	body, err := a.encodeClipboardData(clipData)
	if err != nil {
		slog.Error("编码失败", "错误", err)
		return
	}
	if err := a.sendEncodedBody(body); err != nil {
		slog.Error("发送失败，已加入重发队列", "错误", err)
		a.enqueuePendingSend(pendingSend{body: body, payload: payload, payloadType: payloadType, filename: filename})
		return
	}
	a.appendHistory(payload, payloadType, filename, "sent")
}

func (a *Application) sendEncodedBody(body string) error {
	stomp := a.currentStomp()
	if stomp == nil {
		return fmt.Errorf("not connected")
	}
	if err := stomp.Send(body); err != nil {
		return err
	}
	if p2p := a.currentP2P(); p2p != nil {
		p2p.Send(body)
	}
	return nil
}

// rememberPastedPayload 记录最近一次粘贴到本机的内容，用于回环守卫。
func (a *Application) rememberPastedPayload(payload string) {
	a.pastMu.Lock()
	a.lastPastedPayload = payload
	a.lastPastedTime = time.Now()
	a.pastMu.Unlock()
}

// suppressEcho 报告该复制事件是否由本机粘贴（回环）触发。
func (a *Application) suppressEcho(payload string) bool {
	a.pastMu.Lock()
	defer a.pastMu.Unlock()
	return a.lastPastedPayload == payload && time.Since(a.lastPastedTime) < 5*time.Second
}

// enqueuePendingSend 将失败消息加入重发队列（上限 20 条，超出丢弃最旧）。
func (a *Application) enqueuePendingSend(item pendingSend) {
	a.pendingMu.Lock()
	defer a.pendingMu.Unlock()
	if len(a.pendingSends) >= 20 {
		a.pendingSends = a.pendingSends[1:]
	}
	a.pendingSends = append(a.pendingSends, item)
}

// flushPendingSends 在连接恢复后补发积压消息。
func (a *Application) flushPendingSends() {
	stomp := a.currentStomp()
	if stomp == nil || !stomp.IsConnected() {
		return
	}
	a.pendingMu.Lock()
	if len(a.pendingSends) == 0 || a.pendingFlushing {
		a.pendingMu.Unlock()
		return
	}
	a.pendingFlushing = true
	batch := a.pendingSends
	a.pendingSends = nil
	a.pendingMu.Unlock()
	defer func() {
		a.pendingMu.Lock()
		a.pendingFlushing = false
		a.pendingMu.Unlock()
	}()

	for i, item := range batch {
		if item.body == "" {
			clipData := &protocol.ClipboardData{
				Payload: item.payload, Type: item.payloadType, FileName: item.filename,
				MessageUUID: uuid.New().String(), DeviceID: a.cfg.DeviceID, TimestampMs: time.Now().UnixMilli(),
			}
			body, err := a.encodeClipboardData(clipData)
			if err != nil {
				a.restorePendingSends(batch[i:])
				return
			}
			item.body = body
			batch[i] = item
		}
		if err := a.sendEncodedBody(item.body); err != nil {
			slog.Warn("应用：重发队列投递失败，保留剩余消息", "错误", err)
			a.restorePendingSends(batch[i:])
			return
		}
		a.appendHistory(item.payload, item.payloadType, item.filename, "sent")
	}
	slog.Info("应用：重发队列已清空", "条数", len(batch))
}

func (a *Application) restorePendingSends(items []pendingSend) {
	a.pendingMu.Lock()
	a.pendingSends = append(append([]pendingSend(nil), items...), a.pendingSends...)
	if len(a.pendingSends) > 20 {
		a.pendingSends = a.pendingSends[len(a.pendingSends)-20:]
	}
	a.pendingMu.Unlock()
}

func (a *Application) encodeClipboardData(clipData *protocol.ClipboardData) (string, error) {
	if a.cfg.E2EEEnabled {
		if a.encKey == nil {
			return "", fmt.Errorf("encryption key not ready")
		}
		jsonBytes, _ := clipData.Encode()
		encrypted, err := pkgcrypto.Encrypt(a.encKey, jsonBytes)
		if err != nil {
			return "", err
		}
		return pkgcrypto.EncodeToJSONString(encrypted)
	}
	jsonBytes, _ := clipData.Encode()
	return string(jsonBytes), nil
}

func (a *Application) allowSendType(payloadType string) bool {
	switch payloadType {
	case constants.TypeText:
		return a.cfg.SendText
	case constants.TypeImage:
		return a.cfg.SendImage
	case constants.TypeFileStub, constants.TypeFileEager, constants.TypeFiles:
		return a.cfg.SendFile
	default:
		// 未知类型默认允许，避免未来协议类型被静默吞掉。
		return true
	}
}

// onReceive 当从 server 接收到剪贴板消息时被调用。
func (a *Application) onReceive(body string) {
	clipData, err := a.decodeIncomingClipboardData(body)
	if err != nil {
		slog.Warn("剪贴板数据解码失败", "错误", err)
		return
	}

	now := time.Now()
	a.lastRecvMu.Lock()
	// 1) 丢弃本机设备 ID 的回环消息（服务端已排除 sender，此为 P2P/重连兜底）
	if clipData.DeviceID != "" && clipData.DeviceID == a.cfg.DeviceID {
		a.lastRecvMu.Unlock()
		slog.Debug("应用：丢弃本机设备回环消息")
		return
	}
	// 2) MessageUUID 级去重（跨通道/重连重复投递），窗口 10 分钟，容量上限 256
	if clipData.MessageUUID != "" {
		if _, seen := a.recvUUIDs[clipData.MessageUUID]; seen {
			a.lastRecvMu.Unlock()
			slog.Debug("应用：丢弃重复消息（MessageUUID 已见）")
			return
		}
		a.recvUUIDs[clipData.MessageUUID] = now
		if len(a.recvUUIDs) > 256 {
			for k, t := range a.recvUUIDs {
				if now.Sub(t) > 10*time.Minute {
					delete(a.recvUUIDs, k)
				}
			}
		}
	}
	// 3) 兼容旧客户端：字节级 5 秒单槽去重（双通道并发兜底）
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(body)))
	if a.lastRecvHash == hash && now.Sub(a.lastRecvTime) < 5*time.Second {
		a.lastRecvMu.Unlock()
		slog.Debug("应用：静默丢弃重复荷载（P2P/STOMP 双通道并发兜底保护）")
		return
	}
	a.lastRecvHash = hash
	a.lastRecvTime = now
	a.lastRecvMu.Unlock()

	// Paste to local clipboard
	slog.Info("应用：收到剪贴板更新", "类型", clipData.Type, "大小", sizefmt.HumanSizeFromPayload(clipData.Type, clipData.Payload))
	if clipData.Type == constants.TypeFileOffer {
		a.appendHistory(clipData.Payload, clipData.Type, clipData.FileName, "received")
		go a.handleFileOffer(clipData)
		return
	}
	if clipData.Type == constants.TypeFileRequest {
		go a.handleFileRequest(clipData)
		return
	}
	a.clip.Paste(clipData.Payload, clipData.Type, clipData.FileName)
	a.rememberPastedPayload(clipData.Payload)
	a.appendHistory(clipData.Payload, clipData.Type, clipData.FileName, "received")
	slog.Debug("应用：已接收并粘贴", "类型", clipData.Type, "大小", len(clipData.Payload))
}

func (a *Application) decodeIncomingClipboardData(body string) (*protocol.ClipboardData, error) {
	if a.cfg.E2EEEnabled && a.encKey != nil && looksLikeEncryptedEnvelope(body) {
		encrypted, err := pkgcrypto.DecodeFromJSONString(body)
		if err != nil {
			return nil, err
		}
		plaintext, err := pkgcrypto.Decrypt(a.encKey, encrypted)
		if err != nil {
			return nil, err
		}
		return protocol.DecodeClipboardData(plaintext)
	}
	return protocol.DecodeClipboardData([]byte(body))
}

func looksLikeEncryptedEnvelope(body string) bool {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		return false
	}
	_, hasNonce := raw["nonce"]
	_, hasCiphertext := raw["ciphertext"]
	_, hasTag := raw["tag"]
	return hasNonce && hasCiphertext && hasTag
}

// tryRelayLargeFile 检查 payload 是否为单个大文件，若是则在后台 goroutine 中
// 异步上传并广播 FileOffer，返回 true 表示已接管（onCopy 无需再走 STOMP）。
// 检查本身是同步的（只做 Stat），不会阻塞剪贴板监控 goroutine。
func (a *Application) tryRelayLargeFile(payload string) bool {
	path, ok := extractSingleFilePath(payload)
	if !ok {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if info.Size() <= constants.DefaultFileEagerThresholdBytes {
		return false
	}
	if info.Size() > constants.DefaultFileAutoRelayMaxBytes {
		return false
	}
	go a.relayLargeFile(path, info.Size())
	return true
}

func (a *Application) tryRegisterDeferredFile(payload string) bool {
	path, ok := extractSingleFilePath(payload)
	if !ok {
		return false
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if info.Size() <= constants.DefaultFileAutoRelayMaxBytes {
		return false
	}
	if info.Size() > constants.DefaultFileRelayMaxBytes {
		return false
	}
	go a.registerDeferredFile(path, info.Size())
	return true
}

func (a *Application) relayLargeFile(path string, fileSize int64) {
	transferID := fmt.Sprintf("upload-%d", time.Now().UnixNano())
	a.transferMgr.Start(transferID, transfer.DirectionUpload, filepath.Base(path), fileSize)
	result, err := transfer.UploadFile(a.fileHTTPClient, a.cfg.ServerURL, path, func(sent, total int64) {
		a.transferMgr.UpdateProgress(transferID, sent, total)
	})
	if err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		slog.Error("大文件上传失败", "path", path, "error", err)
		return
	}
	offer := transfer.FileOfferPayload{
		TransferID: transferID,
		RelayID:    result.RelayID,
		FileName:   result.FileName,
		FileSize:   result.FileSize,
		SHA256:     result.SHA256,
		MimeType:   result.MimeType,
		Transport:  "http_relay",
	}
	data, err := json.Marshal(offer)
	if err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		return
	}
	clipData := &protocol.ClipboardData{Payload: string(data), Type: constants.TypeFileOffer, FileName: result.FileName}
	body, err := a.encodeClipboardData(clipData)
	if err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		return
	}
	if err := a.sendEncodedBody(body); err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		return
	}
	a.transferMgr.MarkCompleted(transferID)
}

func (a *Application) registerDeferredFile(path string, fileSize int64) {
	record, err := transfer.RegisterDeferredFile(a.httpClient, a.cfg.ServerURL, &transfer.DeferredRegisterRequest{
		FileName: filepath.Base(path),
		FileSize: fileSize,
		MimeType: detectDeferredMimeType(path),
	})
	if err != nil {
		slog.Error("大文件登记失败", "path", path, "error", err)
		notify("ClipCascade", fmt.Sprintf("大文件登记失败: %s", filepath.Base(path)))
		return
	}
	a.rememberDeferredFile(record.DeferredID, path)
	a.setStatus("Deferred File Ready")
	a.appendHistory(path, constants.TypeFileStub, record.FileName, "sent")
	notify("ClipCascade", fmt.Sprintf("大文件已登记到 Web: %s", record.FileName))
	slog.Info("应用：大文件已登记到 Web 下载列表", "deferred_id", record.DeferredID, "文件名", record.FileName, "大小", fileSize)
}

func (a *Application) handleFileOffer(clipData *protocol.ClipboardData) {
	var offer transfer.FileOfferPayload
	if err := json.Unmarshal([]byte(clipData.Payload), &offer); err != nil {
		slog.Warn("file offer 解析失败", "error", err)
		return
	}
	if offer.RelayID == "" {
		return
	}
	transferID := offer.TransferID
	if transferID == "" {
		transferID = fmt.Sprintf("download-%d", time.Now().UnixNano())
	}
	a.transferMgr.Start(transferID, transfer.DirectionDownload, offer.FileName, offer.FileSize)
	tempDir := filepath.Join(os.TempDir(), "ClipCascade")
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		return
	}
	destPath := filepath.Join(tempDir, fmt.Sprintf("%d-%s", time.Now().UnixNano(), sanitizeOfferName(offer.FileName)))
	if err := transfer.DownloadFile(a.fileHTTPClient, a.cfg.ServerURL, offer.RelayID, destPath, offer.SHA256, func(done, total int64) {
		a.transferMgr.UpdateProgress(transferID, done, total)
	}); err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		return
	}
	a.transferMgr.MarkVerifying(transferID)
	if err := a.clip.PasteLocalFile(destPath); err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		return
	}
	a.transferMgr.MarkCompleted(transferID)
}

func (a *Application) handleFileRequest(clipData *protocol.ClipboardData) {
	var req transfer.DeferredFileRequest
	if err := json.Unmarshal([]byte(clipData.Payload), &req); err != nil {
		slog.Warn("file request 解析失败", "error", err)
		return
	}
	path, ok := a.lookupDeferredFile(req.DeferredID)
	if !ok {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		errMsg := "source file is no longer available"
		if err != nil {
			errMsg = err.Error()
		}
		_, _ = transfer.MarkDeferredFileFailed(a.httpClient, a.cfg.ServerURL, req.DeferredID, errMsg)
		slog.Warn("应用：无法响应延迟下载请求", "deferred_id", req.DeferredID, "error", errMsg)
		return
	}

	transferID := fmt.Sprintf("deferred-upload-%d", time.Now().UnixNano())
	a.transferMgr.Start(transferID, transfer.DirectionUpload, filepath.Base(path), info.Size())
	if _, err := transfer.MarkDeferredFileUploading(a.httpClient, a.cfg.ServerURL, req.DeferredID, 0); err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		_, _ = transfer.MarkDeferredFileFailed(a.httpClient, a.cfg.ServerURL, req.DeferredID, err.Error())
		slog.Error("应用：延迟下载上传状态初始化失败", "deferred_id", req.DeferredID, "error", err)
		return
	}
	lastProgressAt := time.Time{}
	lastProgressBytes := int64(-1)
	result, err := transfer.UploadFile(a.fileHTTPClient, a.cfg.ServerURL, path, func(sent, total int64) {
		a.transferMgr.UpdateProgress(transferID, sent, total)
		if total > 0 && sent < total {
			if sent == lastProgressBytes && time.Since(lastProgressAt) < time.Second {
				return
			}
			if lastProgressAt.IsZero() || time.Since(lastProgressAt) >= time.Second || sent-lastProgressBytes >= 4*1024*1024 {
				if _, progressErr := transfer.MarkDeferredFileUploading(a.httpClient, a.cfg.ServerURL, req.DeferredID, sent); progressErr != nil {
					slog.Warn("应用：回报延迟下载进度失败", "deferred_id", req.DeferredID, "sent", sent, "error", progressErr)
				} else {
					lastProgressAt = time.Now()
					lastProgressBytes = sent
				}
			}
		}
	})
	if err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		_, _ = transfer.MarkDeferredFileFailed(a.httpClient, a.cfg.ServerURL, req.DeferredID, err.Error())
		slog.Error("应用：延迟下载上传失败", "deferred_id", req.DeferredID, "path", path, "error", err)
		return
	}
	if _, err := transfer.MarkDeferredFileReady(a.httpClient, a.cfg.ServerURL, req.DeferredID, result.RelayID); err != nil {
		a.transferMgr.MarkFailed(transferID, err)
		_, _ = transfer.MarkDeferredFileFailed(a.httpClient, a.cfg.ServerURL, req.DeferredID, err.Error())
		slog.Error("应用：延迟下载完成回写失败", "deferred_id", req.DeferredID, "relay_id", result.RelayID, "error", err)
		return
	}
	a.transferMgr.MarkCompleted(transferID)
	notify("ClipCascade", fmt.Sprintf("网页下载已准备: %s", result.FileName))
}

func sanitizeOfferName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "\x00", "")
	if name == "" || name == "." {
		return fmt.Sprintf("clipcascade-file-%d", time.Now().Unix())
	}
	return name
}

func (a *Application) rememberDeferredFile(deferredID string, path string) {
	a.deferredMu.Lock()
	defer a.deferredMu.Unlock()
	// 上限 100 条，超出丢弃最旧（map 无序，此处仅防无界增长）
	if len(a.deferredFiles) >= 100 {
		for k := range a.deferredFiles {
			delete(a.deferredFiles, k)
			break
		}
	}
	a.deferredFiles[deferredID] = path
	a.saveDeferredFilesLocked()
}

// deferredPath 返回 deferred 文件映射的持久化路径。
func (a *Application) deferredPath() string {
	return filepath.Join(config.ConfigDir(), "deferred-files.json")
}

// loadDeferredFiles 启动时恢复 deferred 文件映射（重启后大文件请求不再死链）。
func (a *Application) loadDeferredFiles() {
	data, err := os.ReadFile(a.deferredPath())
	if err != nil {
		return
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil || m == nil {
		return
	}
	// 校验文件仍存在，清理死链
	for id, path := range m {
		if info, err := os.Stat(path); err != nil || info.IsDir() {
			delete(m, id)
		}
	}
	a.deferredFiles = m
}

// saveDeferredFilesLocked 将映射持久化到磁盘（调用方需持有 deferredMu）。
func (a *Application) saveDeferredFilesLocked() {
	data, err := json.MarshalIndent(a.deferredFiles, "", "  ")
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(a.deferredPath()), 0o755); err != nil {
		return
	}
	_ = os.WriteFile(a.deferredPath(), data, 0o600)
}

func (a *Application) lookupDeferredFile(deferredID string) (string, bool) {
	a.deferredMu.Lock()
	defer a.deferredMu.Unlock()
	path, ok := a.deferredFiles[deferredID]
	return path, ok
}

func detectDeferredMimeType(path string) string {
	if mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); mimeType != "" {
		return mimeType
	}
	return "application/octet-stream"
}

func extractSingleFilePath(payload string) (string, bool) {
	paths := make([]string, 0, 1)
	for _, line := range strings.Split(payload, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	if len(paths) != 1 {
		return "", false
	}
	return paths[0], true
}

// currentStomp 安全读取当前 STOMP 客户端（connMu 保护，避免跨 goroutine data race）。
func (a *Application) currentStomp() *transport.StompClient {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.stomp
}

// currentP2P 安全读取当前 P2P 客户端。
func (a *Application) currentP2P() *transport.P2PClient {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.p2p
}

// monitorConnection 检查连接健康状况并触发重连。
func (a *Application) monitorConnection() {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-a.ctx.Done():
			return
		case <-ticker.C:
			stomp := a.currentStomp()
			if stomp != nil && !stomp.IsConnected() && !a.isReconnecting() {
				slog.Warn("应用：连接丢失，正在触发重连")
				go a.reconnectLoop()
			}
			// 断线期间积压的消息在恢复后补发
			a.pendingMu.Lock()
			nPending := len(a.pendingSends)
			a.pendingMu.Unlock()
			if nPending > 0 && stomp != nil && stomp.IsConnected() {
				go a.flushPendingSends()
			}
			// P2P 静默死亡时自动重建（仅 STOMP 已连接时，cookie 仍有效）
			if a.cfg.P2PEnabled && stomp != nil && stomp.IsConnected() && a.currentP2P() == nil {
				if cookies := a.getSessionCookies(); cookies != nil {
					slog.Info("应用：P2P 信令断开，尝试自动重连")
					p2pClient := transport.NewP2PClient(a.cfg.ServerURL, cookies, a.cfg.StunURL)
					p2pClient.OnReceive(a.onReceive)
					a.connMu.Lock()
					a.p2p = p2pClient
					a.connMu.Unlock()
					if err := p2pClient.Connect(); err != nil {
						slog.Warn("应用：P2P 自动重连失败", "错误", err)
						a.connMu.Lock()
						a.p2p = nil
						a.connMu.Unlock()
					}
				}
			}
		}
	}
}

func (a *Application) getSessionCookies() []*http.Cookie {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.sessionCookies
}

// reconnectLoop 会立即发起第一次重连尝试；失败后继续指数退避。
func (a *Application) reconnectLoop() {
	if !a.cfg.AutoReconnect {
		return
	}
	if !a.beginReconnect() {
		return
	}
	defer a.endReconnect()

	a.setStatus("Reconnecting...")

	delay := time.Duration(a.cfg.ReconnectDelay) * time.Second
	if delay == 0 {
		delay = time.Duration(constants.DefaultReconnectDelay) * time.Second
	}
	maxDelay := time.Duration(constants.MaxReconnectDelay) * time.Second

	failCount := 0
	for {
		wait := time.Duration(0)
		if failCount > 0 {
			wait = delay
		}
		select {
		case <-a.ctx.Done():
			return
		case <-time.After(wait):
		}
		a.connMu.Lock()
		wanted := a.connectionDesired
		a.connMu.Unlock()
		if !wanted {
			return
		}
		if stomp := a.currentStomp(); stomp != nil && stomp.IsConnected() {
			return
		}

		failCount++
		slog.Info("应用：正在尝试重连", "延迟", wait, "失败次数", failCount)

		// 先销毁旧传输对象，再按原始 connect 流程重建新的连接。
		a.closeTransports()
		a.connect()
		if stomp := a.currentStomp(); stomp != nil && stomp.IsConnected() {
			return // reconnected
		}

		if failCount > 1 {
			delay = min(delay*2, maxDelay)
		}
	}
}

func (a *Application) beginReconnect() bool {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if a.reconnecting || !a.connectionDesired {
		return false
	}
	a.reconnecting = true
	return true
}

func (a *Application) endReconnect() {
	a.connMu.Lock()
	a.reconnecting = false
	a.connMu.Unlock()
}

func (a *Application) isReconnecting() bool {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	return a.reconnecting
}

// disconnect disconnects from the server.
func (a *Application) disconnect() {
	a.connMu.Lock()
	a.connectionDesired = false
	if a.connectCancel != nil {
		a.connectCancel()
	}
	a.connMu.Unlock()
	a.closeTransports()
	a.setStatus("Disconnected")
	notify("ClipCascade", "Disconnected from server")
	slog.Info("应用：已断开连接")
}

// shutdown cleanly shuts down the application.
func (a *Application) shutdown() {
	a.disconnect()
	a.cancel()
	slog.Info("应用：正在关闭")
}

func (a *Application) closeTransports() {
	a.connMu.Lock()
	defer a.connMu.Unlock()
	if a.stomp != nil {
		a.stomp.Close()
		a.stomp = nil
	}
	if a.p2p != nil {
		a.p2p.Close()
		a.p2p = nil
	}
	a.sessionCookies = nil
}
