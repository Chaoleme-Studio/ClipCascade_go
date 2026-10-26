// Package engine 为 mobile 平台提供 STOMP 客户端引擎。
//
// 此 package 中的函数和类型必须遵循 gomobile 规则：
//   - 仅包含带有导出方法的导出类型
//   - 支持的参数类型：int, float, string, bool, []byte, error
//   - 不得使用 channels、maps 或复杂的 generics
package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	pkgcrypto "github.com/clipcascade/pkg/crypto"
	"github.com/clipcascade/pkg/protocol"
)

// MessageCallback 是 mobile 平台必须实现的接口，
// 用于从 Go engine 接收剪贴板数据。
type MessageCallback interface {
	// 当从其他 device 接收到剪贴板数据时调用 OnMessage。
	// fileName 仅在文件消息中有值。
	OnMessage(payload string, payloadType string, fileName string)

	// 当连接状态改变时调用 OnStatusChange。
	// status: "connected"、"disconnected"、"reconnecting"、"error"
	OnStatusChange(status string)
}

// pendingSend 是发送失败后等待重连补发的消息。
type pendingSend struct {
	payload     string
	payloadType string
	fileName    string
	body        string
}

const (
	pendingSendLimit = 20 // 重发队列上限，超出丢弃最旧
	dedupWindow      = 10 * time.Minute
	dedupCap         = 256
)

// Engine 是用于 mobile 剪贴板同步的主要 Go engine。
type Engine struct {
	mu            sync.Mutex
	writeMu       sync.Mutex
	serverURL     string
	username      string
	password      string
	e2ee          bool
	encKey        []byte
	httpClient    *http.Client
	wsConn        *websocket.Conn
	callback      MessageCallback
	done          chan struct{}
	reconnectWake chan struct{}
	connected     bool
	deviceID      string // 稳定设备标识，用于回环过滤与消息溯源

	pendingMu       sync.Mutex
	pendingSends    []pendingSend
	pendingFlushing bool

	dedupMu   sync.Mutex
	recvUUIDs map[string]time.Time
}

// NewEngine 创建一个新的 ClipCascade mobile engine。
func NewEngine(serverURL, username, password string, e2eeEnabled bool) *Engine {
	jar, _ := cookiejar.New(nil)
	return &Engine{
		serverURL: normalizeServerURL(serverURL),
		username:  username,
		password:  password,
		e2ee:      e2eeEnabled,
		deviceID:  uuid.New().String(),
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: 15 * time.Second,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		done:          make(chan struct{}),
		reconnectWake: make(chan struct{}, 1),
		recvUUIDs:     make(map[string]time.Time),
	}
}

// SetCallback 注册用于接收消息的 mobile 回调。
func (e *Engine) SetCallback(cb MessageCallback) {
	e.callback = cb
}

// Start 连接到 server 并开始监听剪贴板数据。
func (e *Engine) Start() error {
	// Step 1: Login
	if err := e.login(); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	// Step 2: Derive encryption key if E2EE
	if e.e2ee {
		if err := e.deriveKey(); err != nil {
			return fmt.Errorf("key derivation failed: %w", err)
		}
	}

	// Step 3: Connect WebSocket
	if err := e.connectWS(); err != nil {
		return fmt.Errorf("websocket failed: %w", err)
	}

	// Step 4: Disabling P2P on mobile to bypass Android NDK 1.22+ zoneCache compilation bugs.
	// STOMP relay will handle all syncing perfectly for mobile.

	e.setConnected(true)
	e.notifyStatus("connected")
	go e.readLoop()
	go e.heartbeatLoop()
	return nil
}

// Stop 从 server 断开连接。
func (e *Engine) Stop() {
	var conn *websocket.Conn
	e.mu.Lock()
	select {
	case <-e.done:
	default:
		close(e.done)
	}

	conn = e.wsConn
	e.wsConn = nil
	e.connected = false
	e.mu.Unlock()

	if conn != nil {
		_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""), time.Now().Add(time.Second))
		_ = conn.Close()
	}
	e.notifyStatus("disconnected")
}

// ReconnectNow 唤醒退避中的单个重连循环；网络切换时关闭旧网络上的死连接。
func (e *Engine) ReconnectNow() {
	if e.isStopped() {
		return
	}
	select {
	case e.reconnectWake <- struct{}{}:
	default:
	}
	e.mu.Lock()
	conn := e.wsConn
	e.connected = false
	e.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// SendClipboard 向 server 发送剪贴板数据。
func (e *Engine) SendClipboard(payload string, payloadType string) error {
	return e.SendClipboardWithFileName(payload, payloadType, "")
}

// SendClipboardWithFileName 向 server 发送带原始文件名的剪贴板数据。
// 连接不可用时消息进入重发队列，重连成功后自动补发。
func (e *Engine) SendClipboardWithFileName(payload string, payloadType string, fileName string) error {
	item := pendingSend{payload: payload, payloadType: payloadType, fileName: fileName}
	body, err := e.encodeClipboardBody(item)
	if err != nil {
		e.enqueuePendingSend(item)
		return err
	}
	item.body = body
	item.payload = ""
	if err := e.sendEncodedBody(body); err != nil {
		e.enqueuePendingSend(item)
		return err
	}
	return nil
}

func (e *Engine) encodeClipboardBody(item pendingSend) (string, error) {
	e.mu.Lock()
	encKey := e.encKey
	e2ee := e.e2ee
	deviceID := e.deviceID
	e.mu.Unlock()
	if e2ee && encKey == nil {
		return "", fmt.Errorf("encryption key not ready")
	}

	clipData := &protocol.ClipboardData{
		Payload:     item.payload,
		Type:        item.payloadType,
		FileName:    item.fileName,
		MessageUUID: uuid.New().String(),
		DeviceID:    deviceID,
		TimestampMs: time.Now().UnixMilli(),
	}
	var body string

	if e2ee {
		jsonBytes, _ := clipData.Encode()
		encrypted, err := pkgcrypto.Encrypt(encKey, jsonBytes)
		if err != nil {
			return "", err
		}
		body, _ = pkgcrypto.EncodeToJSONString(encrypted)
	} else {
		jsonBytes, _ := clipData.Encode()
		body = string(jsonBytes)
	}

	return body, nil
}

func (e *Engine) sendEncodedBody(body string) error {
	e.mu.Lock()
	conn := e.wsConn
	e.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("not connected (queued for retry)")
	}
	sendFrame := protocol.SendFrame("/app/cliptext", body)
	e.writeMu.Lock()
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	err := conn.WriteMessage(websocket.TextMessage, sendFrame.Encode())
	e.writeMu.Unlock()
	if err != nil {
		_ = conn.Close() // 唤醒 readLoop 立即进入重连，不能仅入队后继续持有死连接。
	}
	return err
}

// enqueuePendingSend 将失败消息加入重发队列（上限 20 条，超出丢弃最旧）。
func (e *Engine) enqueuePendingSend(item pendingSend) {
	e.pendingMu.Lock()
	defer e.pendingMu.Unlock()
	if len(e.pendingSends) >= pendingSendLimit {
		e.pendingSends = e.pendingSends[1:]
	}
	e.pendingSends = append(e.pendingSends, item)
}

// flushPendingSends 重连成功后补发积压消息。
func (e *Engine) flushPendingSends() {
	e.pendingMu.Lock()
	if len(e.pendingSends) == 0 || e.pendingFlushing {
		e.pendingMu.Unlock()
		return
	}
	e.pendingFlushing = true
	batch := e.pendingSends
	e.pendingSends = nil
	e.pendingMu.Unlock()
	defer func() {
		e.pendingMu.Lock()
		e.pendingFlushing = false
		e.pendingMu.Unlock()
	}()

	for i, item := range batch {
		if item.body == "" {
			body, err := e.encodeClipboardBody(item)
			if err != nil {
				e.restorePendingSends(batch[i:])
				return
			}
			item.body = body
			item.payload = ""
			batch[i] = item
		}
		if err := e.sendEncodedBody(item.body); err != nil {
			log.Println("bridge: pending send retry failed, keeping remaining messages:", err)
			e.restorePendingSends(batch[i:])
			return
		}
	}
	log.Printf("bridge: pending sends flushed (%d items)", len(batch))
}

func (e *Engine) restorePendingSends(items []pendingSend) {
	e.pendingMu.Lock()
	e.pendingSends = append(append([]pendingSend(nil), items...), e.pendingSends...)
	if len(e.pendingSends) > pendingSendLimit {
		e.pendingSends = e.pendingSends[len(e.pendingSends)-pendingSendLimit:]
	}
	e.pendingMu.Unlock()
}

// IsConnected 返回 engine 是否已连接。
func (e *Engine) IsConnected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.connected
}

func (e *Engine) DeviceCounts() (onlineDevices int, syncTargets int, err error) {
	serverURL := normalizeServerURL(e.serverURL)
	resp, err := e.httpClient.Get(serverURL + "/api/devices/online")
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, 0, fmt.Errorf("device status failed: %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	}
	var body struct {
		OnlineDevices int `json:"online_devices"`
		SyncTargets   int `json:"sync_targets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return 0, 0, err
	}
	return body.OnlineDevices, body.SyncTargets, nil
}

// --- Internal methods ---

func (e *Engine) login() error {
	form := url.Values{
		"username": {e.username},
		"password": {e.password},
	}
	resp, err := e.httpClient.PostForm(e.serverURL+"/login", form)
	if err != nil {
		return err
	}
	resp.Body.Close()

	if resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther {
		loc := resp.Header.Get("Location")
		if strings.Contains(loc, "error") {
			return fmt.Errorf("invalid credentials")
		}
	} else {
		return fmt.Errorf("login returned HTTP %d", resp.StatusCode)
	}
	u, _ := url.Parse(e.serverURL)
	if len(e.httpClient.Jar.Cookies(u)) == 0 {
		return fmt.Errorf("no session cookie received")
	}
	return nil
}

func (e *Engine) deriveKey() error {
	serverURL := normalizeServerURL(e.serverURL)
	resp, err := e.httpClient.Get(serverURL + "/api/user-info")
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var info struct {
		Salt       string `json:"salt"`
		HashRounds int    `json:"hash_rounds"`
	}
	// 修复记录：旧实现忽略了解码错误，失败时会静默用零值 salt 派生错误密钥
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return fmt.Errorf("decode user-info: %w", err)
	}
	if info.Salt == "" {
		info.Salt = e.username // 与服务端默认行为一致
	}
	if info.HashRounds <= 0 {
		info.HashRounds = 100000
	}
	key := pkgcrypto.DeriveKey(e.password, e.username, info.Salt, info.HashRounds)
	e.mu.Lock()
	e.encKey = key
	e.mu.Unlock()
	return nil
}

func (e *Engine) connectWS() error {
	serverURL := normalizeServerURL(e.serverURL)
	u, _ := url.Parse(serverURL)
	scheme := "ws"
	if u.Scheme == "https" {
		scheme = "wss"
	}
	wsURL := fmt.Sprintf("%s://%s/clipsocket", scheme, u.Host)

	header := http.Header{}
	for _, c := range e.httpClient.Jar.Cookies(u) {
		header.Add("Cookie", c.String())
	}

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}
	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))

	// STOMP handshake（修复记录：旧实现忽略握手期的全部写错误）
	if err := conn.WriteMessage(websocket.TextMessage, protocol.ConnectFrame("1.1", "localhost").Encode()); err != nil {
		conn.Close()
		return fmt.Errorf("STOMP CONNECT write: %w", err)
	}
	_, msg, err := conn.ReadMessage()
	if err != nil {
		conn.Close()
		return fmt.Errorf("STOMP CONNECT read: %w", err)
	}
	frame, _ := protocol.ParseFrame(msg)
	if frame == nil || frame.Command != "CONNECTED" {
		conn.Close()
		return fmt.Errorf("STOMP handshake failed")
	}

	if err := conn.WriteMessage(websocket.TextMessage, protocol.SubscribeFrame("sub-0", "/user/queue/cliptext").Encode()); err != nil {
		conn.Close()
		return fmt.Errorf("STOMP SUBSCRIBE write: %w", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(35 * time.Second))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(35 * time.Second))
	})

	e.mu.Lock()
	if e.isStopped() {
		e.mu.Unlock()
		_ = conn.Close()
		return fmt.Errorf("stopped")
	}
	old := e.wsConn
	e.wsConn = conn
	e.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	return nil
}

func normalizeServerURL(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return strings.TrimRight(s, "/")
	}
	if u.Scheme == "" {
		u.Scheme = "http"
	}
	return strings.TrimRight(u.String(), "/")
}

func (e *Engine) readLoop() {
	for {
		if e.isStopped() {
			return
		}

		e.mu.Lock()
		conn := e.wsConn
		e.mu.Unlock()
		if conn == nil {
			if err := e.reconnectLoop(); err != nil {
				e.notifyStatus("disconnected")
				return
			}
			continue
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			log.Println("bridge: ws read error:", err)
			if e.isStopped() {
				return
			}
			e.setConnected(false)
			e.notifyStatus("reconnecting")
			if rErr := e.reconnectLoop(); rErr != nil {
				e.notifyStatus("disconnected")
				return
			}
			continue
		}
		_ = conn.SetReadDeadline(time.Now().Add(35 * time.Second))

		frame, err := protocol.ParseFrame(msg)
		if err != nil || frame.Command != "MESSAGE" {
			continue
		}

		e.handleIncomingData(frame.Body)
	}
}

func (e *Engine) reconnectLoop() error {
	backoff := 2 * time.Second
	for {
		if e.isStopped() {
			return fmt.Errorf("stopped")
		}

		if err := e.login(); err == nil {
			if !e.e2ee || e.deriveKey() == nil {
				if err := e.connectWS(); err == nil {
					e.setConnected(true)
					e.notifyStatus("connected")
					// 重连成功后补发断线期间积压的消息
					go e.flushPendingSends()
					return nil
				}
			}
		}

		select {
		case <-e.done:
			return fmt.Errorf("stopped")
		case <-e.reconnectWake:
			backoff = 2 * time.Second
			continue
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, 30*time.Second)
	}
}

func (e *Engine) heartbeatLoop() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.done:
			return
		case <-ticker.C:
			e.mu.Lock()
			conn := e.wsConn
			e.mu.Unlock()
			if conn == nil {
				continue
			}

			// Ping/Pong 能检测半开连接；仅写入 LF 无法确认对端仍在响应。
			err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			if err != nil {
				log.Println("bridge: heartbeat failed:", err)
				_ = conn.Close() // readLoop will detect and reconnect
			}
		}
	}
}

func (e *Engine) setConnected(v bool) {
	e.mu.Lock()
	e.connected = v
	e.mu.Unlock()
}

func (e *Engine) notifyStatus(status string) {
	if e.callback != nil {
		e.callback.OnStatusChange(status)
	}
}

func (e *Engine) isStopped() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

func (e *Engine) handleIncomingData(body string) {
	var clipData *protocol.ClipboardData

	e.mu.Lock()
	encKey := e.encKey
	e.mu.Unlock()
	if e.e2ee && encKey != nil && looksLikeEncryptedEnvelope(body) {
		encrypted, err := pkgcrypto.DecodeFromJSONString(body)
		if err != nil {
			return
		}
		plaintext, err := pkgcrypto.Decrypt(encKey, encrypted)
		if err != nil {
			return
		}
		clipData, _ = protocol.DecodeClipboardData(plaintext)
	} else {
		clipData, _ = protocol.DecodeClipboardData([]byte(body))
	}

	if clipData == nil || e.callback == nil {
		return
	}

	// 回环过滤：丢弃本机设备发出的消息
	if clipData.DeviceID != "" && clipData.DeviceID == e.deviceID {
		return
	}
	// MessageUUID 去重（重连/重投递兜底）
	if clipData.MessageUUID != "" {
		now := time.Now()
		e.dedupMu.Lock()
		if _, seen := e.recvUUIDs[clipData.MessageUUID]; seen {
			e.dedupMu.Unlock()
			return
		}
		e.recvUUIDs[clipData.MessageUUID] = now
		if len(e.recvUUIDs) > dedupCap {
			for k, t := range e.recvUUIDs {
				if now.Sub(t) > dedupWindow {
					delete(e.recvUUIDs, k)
				}
			}
		}
		e.dedupMu.Unlock()
	}

	e.callback.OnMessage(clipData.Payload, clipData.Type, clipData.FileName)
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
