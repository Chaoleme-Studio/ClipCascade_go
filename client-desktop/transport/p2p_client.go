// Package transport 提供 P2P WebRTC DataChannel client，用于
// 设备到设备之间的直接剪贴板同步，绕过 server 进行数据传输。
package transport

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/clipcascade/pkg/constants"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/pion/webrtc/v4"
	// "github.com/clipcascade/pkg/protocol" is now cleanly bypassed so the P2P layer won't falsely escape E2EE JSON blocks
)

// P2PClient 管理用于 P2P 剪贴板同步的 WebRTC peer 连接。
type P2PClient struct {
	serverURL          string
	cookies            []*http.Cookie
	wsConn             *websocket.Conn
	mu                 sync.Mutex
	peers              map[string]*webrtc.PeerConnection // sessionID → PeerConnection
	dataChans          map[string]*webrtc.DataChannel    // sessionID → DataChannel
	sessionID          string
	stunURL            string
	onReceive          func(data string)
	receivingFragments map[string][]string  // ID -> fragments
	fragmentTimes      map[string]time.Time // ID -> 首个分片到达时间（超时清理用）
	pendingPeerLists   [][]string           // session-id 分配前到达的 peer-list 缓存（glare 修复）
	done               chan struct{}
	wsMu               sync.Mutex // 防止 Gorilla WebSocket 发生并发死写 panic
}

// p2pFragment 是 P2P 专用分片包装结构（不使用 protocol.ClipboardData
// 避免与应用层/E2EE 双重嵌套）。
type p2pFragment struct {
	Payload        string `json:"payload"`
	ID             string `json:"id"`
	Index          int    `json:"index"`
	TotalFragments int    `json:"totalFragments"`
}

const (
	p2pReassembleTimeout   = 60 * time.Second // 不完整分片的淘汰时限
	p2pSignalReconnectBase = 5 * time.Second  // 信令重连退避起点
	p2pSignalReconnectMax  = 60 * time.Second // 信令重连退避上限
	maxP2PFragments        = 4096             // 每条消息最多 64 MiB，限制恶意分片声明占用内存
	maxP2PFragmentGroups   = 128              // 同时缓存的不完整消息数
)

// NewP2PClient 创建一个连接到 signaling server 的 P2P client。
func NewP2PClient(serverURL string, cookies []*http.Cookie, stunURL string) *P2PClient {
	return &P2PClient{
		serverURL:          serverURL,
		cookies:            cookies,
		stunURL:            stunURL,
		peers:              make(map[string]*webrtc.PeerConnection),
		dataChans:          make(map[string]*webrtc.DataChannel),
		receivingFragments: make(map[string][]string),
		fragmentTimes:      make(map[string]time.Time),
		done:               make(chan struct{}),
	}
}

// OnReceive 设置通过 DataChannel 接收到数据时的回调。
func (p *P2PClient) OnReceive(fn func(data string)) {
	p.mu.Lock()
	p.onReceive = fn
	p.mu.Unlock()
}

func (p *P2PClient) getOnReceive() func(data string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.onReceive
}

func (p *P2PClient) getWSConn() *websocket.Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.wsConn
}

// Connect 建立 signaling WebSocket 连接。断线后由 signalLoop 内部自动重连。
func (p *P2PClient) Connect() error {
	conn, err := p.dialSignaling()
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.wsConn = conn
	p.mu.Unlock()

	go p.signalLoop()

	slog.Info("p2p: signaling connected")
	return nil
}

// dialSignaling 建立一条新的信令 WebSocket 连接。
func (p *P2PClient) dialSignaling() (*websocket.Conn, error) {
	wsURL, err := p.buildWSURL()
	if err != nil {
		return nil, err
	}

	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	header := http.Header{}
	for _, c := range p.cookies {
		header.Add("Cookie", c.String())
	}

	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("p2p signaling dial: %w", err)
	}
	return conn, nil
}

// Send 通过 DataChannel 向所有已连接的 peers 广播数据。
// 支持自动分片以处理大数据包。发送过程不持有 p.mu，避免阻塞接收路径。
func (p *P2PClient) Send(data string) {
	p.mu.Lock()
	chans := make([]*webrtc.DataChannel, 0, len(p.dataChans))
	for _, dc := range p.dataChans {
		if dc.ReadyState() == webrtc.DataChannelStateOpen {
			chans = append(chans, dc)
		}
	}
	p.mu.Unlock()

	fragmentID := uuid.New().String()
	dataBytes := []byte(data)
	totalSize := len(dataBytes)

	// 计算分片
	numFragments := (totalSize + constants.FragmentSize - 1) / constants.FragmentSize
	if numFragments > maxP2PFragments {
		slog.Warn("p2p: 消息超过分片上限，跳过 P2P 发送", "fragments", numFragments)
		return
	}
	if numFragments == 0 {
		return
	}

	for i := 0; i < numFragments; i++ {
		start := i * constants.FragmentSize
		end := start + constants.FragmentSize
		if end > totalSize {
			end = totalSize
		}

		frag := &p2pFragment{
			Payload:        string(dataBytes[start:end]),
			ID:             fragmentID,
			Index:          i,
			TotalFragments: numFragments,
		}

		encoded, _ := json.Marshal(frag)

		for _, dc := range chans {
			if err := dc.SendText(string(encoded)); err != nil {
				slog.Warn("p2p: send error", "error", err)
			}
		}
	}
}

// Close 关闭所有 peer 连接和 signaling。
func (p *P2PClient) Close() {
	p.mu.Lock()
	select {
	case <-p.done:
	default:
		close(p.done)
	}
	p.mu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	for _, pc := range p.peers {
		pc.Close()
	}
	p.peers = make(map[string]*webrtc.PeerConnection)
	p.dataChans = make(map[string]*webrtc.DataChannel)

	if p.wsConn != nil {
		p.wsConn.Close()
		p.wsConn = nil
	}
}

// signalLoop 维护信令连接：读取消息，断线后自动指数退避重连。
func (p *P2PClient) signalLoop() {
	for {
		select {
		case <-p.done:
			return
		default:
		}

		conn := p.getWSConn()
		if conn == nil {
			// 尚未连接（理论不应发生），尝试重拨
			if c, err := p.dialSignaling(); err == nil {
				p.mu.Lock()
				p.wsConn = c
				p.mu.Unlock()
				conn = c
			} else {
				if !p.waitReconnect(p2pSignalReconnectBase) {
					return
				}
				continue
			}
		}

		p.readSignals(conn)

		// 读循环退出 → 连接断开，进入退避重连
		select {
		case <-p.done:
			return
		default:
		}
		if !p.waitReconnect(p2pSignalReconnectBase) {
			return
		}

		reconnected := false
		delay := p2pSignalReconnectBase
		for attempt := 0; attempt < 10; attempt++ {
			if c, err := p.dialSignaling(); err == nil {
				p.mu.Lock()
				p.wsConn = c
				p.mu.Unlock()
				slog.Info("p2p: signaling reconnected")
				reconnected = true
				break
			}
			if !p.waitReconnect(delay) {
				return
			}
			delay = min(delay*2, p2pSignalReconnectMax)
		}
		if !reconnected {
			slog.Warn("p2p: signaling reconnection gave up (will retry via app monitor)")
			return
		}
	}
}

// waitReconnect 等待 d 或 done；返回 false 表示已关闭。
func (p *P2PClient) waitReconnect(d time.Duration) bool {
	select {
	case <-p.done:
		return false
	case <-time.After(d):
		return true
	}
}

// readSignals 从信令连接读取并分发消息，直到出错或关闭。
func (p *P2PClient) readSignals(conn *websocket.Conn) {
	const readTimeout = 120 * time.Second
	const pingInterval = 30 * time.Second

	heartbeatDone := make(chan struct{})
	defer close(heartbeatDone)

	conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		return nil
	})

	defer func() {
		if r := recover(); r != nil {
			slog.Warn("p2p: signal loop panic recovered", "panic", r)
		}
		p.mu.Lock()
		if p.wsConn == conn {
			p.wsConn = nil
		}
		p.mu.Unlock()
	}()

	// 启动 P2P 信令心跳
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-p.done:
				return
			case <-heartbeatDone:
				return
			case <-ticker.C:
				p.mu.Lock()
				connClosed := p.wsConn != conn || p.wsConn == nil
				p.mu.Unlock()
				if connClosed {
					return
				}
				p.wsMu.Lock()
				_ = conn.WriteMessage(websocket.PingMessage, nil)
				p.wsMu.Unlock()
			}
		}
	}()

	for {
		select {
		case <-p.done:
			return
		default:
		}

		_, msg, err := conn.ReadMessage()
		if err != nil {
			slog.Warn("p2p: signaling read error", "error", err)
			return
		}

		conn.SetReadDeadline(time.Now().Add(readTimeout))

		var signal struct {
			Type      string          `json:"type"`
			From      string          `json:"from"`
			To        string          `json:"to"`
			SessionID string          `json:"session_id"`
			Data      json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(msg, &signal); err != nil {
			continue
		}

		switch signal.Type {
		case "session-id":
			p.mu.Lock()
			p.sessionID = signal.SessionID
			pending := p.pendingPeerLists
			p.pendingPeerLists = nil
			p.mu.Unlock()
			slog.Info("p2p: assigned session", "id", p.sessionID)
			// 处理 session-id 分配前缓存的 peer-list（glare 修复）
			for _, peers := range pending {
				p.handlePeerList(peers)
			}

		case "peer-list":
			var peers []string
			json.Unmarshal(signal.Data, &peers)
			p.mu.Lock()
			noSession := p.sessionID == ""
			if noSession {
				p.pendingPeerLists = append(p.pendingPeerLists, peers)
			}
			p.mu.Unlock()
			if !noSession {
				p.handlePeerList(peers)
			}

		case "offer":
			p.handleOffer(signal.From, signal.Data)

		case "answer":
			p.handleAnswer(signal.From, signal.Data)

		case "ice-candidate":
			p.handleICECandidate(signal.From, signal.Data)
		}
	}
}

// handlePeerList 为新 peers 创建 offers。
func (p *P2PClient) handlePeerList(peers []string) {
	for _, peerID := range peers {
		if peerID == p.sessionID {
			continue
		}
		p.mu.Lock()
		_, exists := p.peers[peerID]
		p.mu.Unlock()
		if !exists {
			// WebRTC Glare 修复：使用字典序比较分配发起者/接受者身份
			// 只有 sessionID 大的一方才发起提议，另一方静默等待被叫。
			if p.sessionID > peerID {
				go p.createOffer(peerID)
			}
		}
	}
}

// createOffer 发起 peer 连接并发送 SDP offer。
func (p *P2PClient) createOffer(peerID string) {
	pc, err := p.newPeerConnection(peerID)
	if err != nil {
		slog.Warn("p2p: create peer connection failed", "error", err)
		return
	}

	// 创建 DataChannel
	dc, err := pc.CreateDataChannel("clipboard", nil)
	if err != nil {
		slog.Warn("p2p: create data channel failed", "error", err)
		return
	}
	p.setupDataChannel(peerID, dc)

	// 创建并设置本地 offer
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		return
	}
	pc.SetLocalDescription(offer)

	// 通过 signaling 发送 offer
	offerJSON, _ := json.Marshal(offer)
	p.sendSignal("offer", peerID, offerJSON)
}

// handleOffer 处理传入的 SDP offer。同一 peer 重复 offer 时先关闭旧连接防泄漏。
func (p *P2PClient) handleOffer(from string, data json.RawMessage) {
	p.mu.Lock()
	if old, exists := p.peers[from]; exists {
		delete(p.peers, from)
		delete(p.dataChans, from)
		go old.Close()
	}
	p.mu.Unlock()

	pc, err := p.newPeerConnection(from)
	if err != nil {
		return
	}

	// 处理传入的 DataChannel
	pc.OnDataChannel(func(dc *webrtc.DataChannel) {
		p.setupDataChannel(from, dc)
	})

	var offer webrtc.SessionDescription
	json.Unmarshal(data, &offer)
	pc.SetRemoteDescription(offer)

	answer, _ := pc.CreateAnswer(nil)
	pc.SetLocalDescription(answer)

	answerJSON, _ := json.Marshal(answer)
	p.sendSignal("answer", from, answerJSON)
}

// handleAnswer 处理传入的 SDP answer。
func (p *P2PClient) handleAnswer(from string, data json.RawMessage) {
	p.mu.Lock()
	pc, ok := p.peers[from]
	p.mu.Unlock()
	if !ok {
		return
	}

	var answer webrtc.SessionDescription
	json.Unmarshal(data, &answer)
	pc.SetRemoteDescription(answer)
}

// handleICECandidate 添加来自 peer 的 ICE candidate。
func (p *P2PClient) handleICECandidate(from string, data json.RawMessage) {
	p.mu.Lock()
	pc, ok := p.peers[from]
	p.mu.Unlock()
	if !ok {
		return
	}

	var candidate webrtc.ICECandidateInit
	json.Unmarshal(data, &candidate)

	// WebRTC 竞争条件修复: 如果尚未设置 RemoteDescription，AddICECandidate 将因为 ufrag 错误而失败。
	// 我们启动一个 goroutine 等待它被设置完成。
	go func() {
		for i := 0; i < 50; i++ {
			if pc.RemoteDescription() != nil {
				err := pc.AddICECandidate(candidate)
				if err != nil {
					slog.Warn("p2p: 添加 ICE candidate 失败", "错误", err)
				}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		slog.Warn("p2p: 等待 remote description 添加 ICE candidate 超时")
	}()
}

// newPeerConnection 使用 STUN 配置创建一个新的 WebRTC PeerConnection。
func (p *P2PClient) newPeerConnection(peerID string) (*webrtc.PeerConnection, error) {
	config := webrtc.Configuration{
		ICEServers: []webrtc.ICEServer{
			{URLs: []string{p.stunURL}},
		},
	}

	pc, err := webrtc.NewPeerConnection(config)
	if err != nil {
		return nil, err
	}

	// 处理 ICE candidates
	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		candidateJSON, _ := json.Marshal(c.ToJSON())
		p.sendSignal("ice-candidate", peerID, candidateJSON)
	})

	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		slog.Debug("p2p: connection state", "peer", peerID, "state", state)
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			p.mu.Lock()
			delete(p.peers, peerID)
			delete(p.dataChans, peerID)
			p.mu.Unlock()
		}
	})

	p.mu.Lock()
	p.peers[peerID] = pc
	p.mu.Unlock()

	return pc, nil
}

// setupDataChannel 配置 DataChannel 回调。
func (p *P2PClient) setupDataChannel(peerID string, dc *webrtc.DataChannel) {
	dc.OnOpen(func() {
		slog.Info("p2p: data channel open", "peer", peerID)
		p.mu.Lock()
		p.dataChans[peerID] = dc
		p.mu.Unlock()
	})

	dc.OnMessage(func(msg webrtc.DataChannelMessage) {
		defer func() {
			if r := recover(); r != nil {
				slog.Warn("p2p: data channel message panic recovered", "panic", r)
			}
		}()

		var frag p2pFragment
		if err := json.Unmarshal(msg.Data, &frag); err != nil || frag.TotalFragments == 0 {
			// 如果不是我们期望的分片 JSON 格式，尝试作为原始数据处理
			if cb := p.getOnReceive(); cb != nil {
				cb(string(msg.Data))
			}
			return
		}

		if frag.TotalFragments > maxP2PFragments {
			slog.Warn("p2p: 丢弃超出分片上限的消息", "fragments", frag.TotalFragments)
			return
		}
		if frag.TotalFragments <= 1 {
			// 单包直接透传原始数据
			if cb := p.getOnReceive(); cb != nil {
				cb(frag.Payload)
			}
			return
		}

		// 越界校验：恶意/乱序分片不得触发 index-out-of-range
		if frag.Index < 0 || frag.Index >= frag.TotalFragments {
			slog.Warn("p2p: 丢弃越界分片", "id", frag.ID, "index", frag.Index, "total", frag.TotalFragments)
			return
		}

		// 分片重组在锁内完成，回调在锁外执行，避免阻塞其他 peer 的接收
		fullPayload, complete := func() (string, bool) {
			p.mu.Lock()
			defer p.mu.Unlock()

			cur, ok := p.receivingFragments[frag.ID]
			if !ok || len(cur) != frag.TotalFragments {
				if !ok && len(p.receivingFragments) >= maxP2PFragmentGroups {
					var oldestID string
					var oldestTime time.Time
					for id, receivedAt := range p.fragmentTimes {
						if oldestID == "" || receivedAt.Before(oldestTime) {
							oldestID, oldestTime = id, receivedAt
						}
					}
					delete(p.receivingFragments, oldestID)
					delete(p.fragmentTimes, oldestID)
				}
				cur = make([]string, frag.TotalFragments)
				p.receivingFragments[frag.ID] = cur
				p.fragmentTimes[frag.ID] = time.Now()
			}
			cur[frag.Index] = frag.Payload

			for _, f := range cur {
				if f == "" {
					return "", false
				}
			}
			// 组装完整的原生 payload (已由应用层加密/打包)，直接透传，切勿二次 json.Marshal!
			full := strings.Join(cur, "")
			delete(p.receivingFragments, frag.ID)
			delete(p.fragmentTimes, frag.ID)
			return full, true
		}()

		if complete {
			if cb := p.getOnReceive(); cb != nil {
				cb(fullPayload)
			}
			return
		}
		p.pruneStaleFragments()
	})

	dc.OnClose(func() {
		slog.Info("p2p: data channel closed", "peer", peerID)
		p.mu.Lock()
		delete(p.dataChans, peerID)
		p.mu.Unlock()
	})
}

// pruneStaleFragments 清理超过时限仍未重组完成的残片，防止内存泄漏。
func (p *P2PClient) pruneStaleFragments() {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	for id, t := range p.fragmentTimes {
		if now.Sub(t) > p2pReassembleTimeout {
			delete(p.receivingFragments, id)
			delete(p.fragmentTimes, id)
			slog.Warn("p2p: 重组超时，丢弃不完整分片组", "id", id)
		}
	}
}

// sendSignal 通过 signaling server 向特定 peer 发送 signaling 消息。
func (p *P2PClient) sendSignal(msgType, to string, data json.RawMessage) {
	msg, _ := json.Marshal(map[string]interface{}{
		"type": msgType,
		"to":   to,
		"data": data,
	})

	p.wsMu.Lock()
	if conn := p.getWSConn(); conn != nil {
		_ = conn.WriteMessage(websocket.TextMessage, msg)
	}
	p.wsMu.Unlock()
}

func (p *P2PClient) buildWSURL() (string, error) {
	u, err := url.Parse(p.serverURL)
	if err != nil {
		return "", err
	}
	scheme := "ws"
	if strings.HasPrefix(u.Scheme, "https") {
		scheme = "wss"
	}
	return fmt.Sprintf("%s://%s/p2p", scheme, u.Host), nil
}
