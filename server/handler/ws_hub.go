package handler

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/gofiber/contrib/websocket"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/pkg/protocol"
	"github.com/clipcascade/pkg/sizefmt"
	"github.com/clipcascade/server/automation"
)

// WSHub 管理所有活动的 WebSocket 连接并在 users 之间路由 STOMP 消息。
// 每个 user 可以有多个连接（即多个 devices）；当一个 device 发送剪贴板数据时，
// 同一个 user 的所有其他 devices 都会收到该数据。
type WSHub struct {
	// mu 保护 connections map。
	mu sync.RWMutex
	// connections 映射 username → *websocket.Conn 集合
	connections map[string]map[*websocket.Conn]bool
	// writeLocks 避免同一连接被并发写导致 gorilla/websocket panic。
	writeLocks map[*websocket.Conn]*sync.Mutex

	// Stats
	totalConnections  atomic.Int64 // 累计总量
	totalInboundMsgs  atomic.Int64
	totalOutboundMsgs atomic.Int64
	automation        *automation.Engine
	// readLimit 是单条 WS 消息的最大字节数，超限直接断开（防内存耗尽 DoS）。
	readLimit int64
}

// NewWSHub 创建一个新的 WebSocket 连接 hub。
func NewWSHub() *WSHub {
	return &WSHub{
		connections: make(map[string]map[*websocket.Conn]bool),
		writeLocks:  make(map[*websocket.Conn]*sync.Mutex),
	}
}

func (h *WSHub) SetAutomationEngine(engine *automation.Engine) {
	h.automation = engine
}

// SetReadLimit 设置单条 WS 消息的最大字节数。
func (h *WSHub) SetReadLimit(limit int64) {
	h.readLimit = limit
}

// Register 为给定的 username 添加一个连接。
// 返回 false 表示超过单用户连接数上限，连接未注册。
func (h *WSHub) Register(username string, conn *websocket.Conn) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.connections[username] == nil {
		h.connections[username] = make(map[*websocket.Conn]bool)
	}
	if len(h.connections[username]) >= constants.MaxWSConnsPerUser {
		slog.Warn("WS：超过单用户连接数上限，拒绝连接", "用户名", username, "上限", constants.MaxWSConnsPerUser, "IP", conn.RemoteAddr().String())
		return false
	}
	h.connections[username][conn] = true
	h.writeLocks[conn] = &sync.Mutex{}
	h.totalConnections.Add(1)
	slog.Info("WS：客户端已连接", "用户名", username, "IP", conn.RemoteAddr().String(), "当前活动连接数", len(h.connections[username]))
	return true
}

// Unregister 为给定的 username 移除一个连接。
func (h *WSHub) Unregister(username string, conn *websocket.Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if conns, ok := h.connections[username]; ok {
		delete(conns, conn)
		if len(conns) == 0 {
			delete(h.connections, username)
		}
	}
	delete(h.writeLocks, conn)
	slog.Info("WS：客户端已断开", "用户名", username, "IP", conn.RemoteAddr().String())
}

// Broadcast 向给定 user 的所有连接发送消息，但 sender 连接除外。
// 这实现了“同步到我的所有其他 devices”的逻辑。
func (h *WSHub) Broadcast(username string, sender *websocket.Conn, data []byte) {
	h.mu.RLock()
	conns, ok := h.connections[username]
	if !ok {
		h.mu.RUnlock()
		return
	}
	type target struct {
		conn *websocket.Conn
		mu   *sync.Mutex
	}
	targets := make([]target, 0, len(conns))
	for conn := range conns {
		if conn == sender {
			continue // 不要回显给 sender
		}
		targets = append(targets, target{
			conn: conn,
			mu:   h.writeLocks[conn],
		})
	}
	h.mu.RUnlock()

	for _, t := range targets {
		h.totalOutboundMsgs.Add(1)
		if t.mu == nil {
			continue
		}
		t.mu.Lock()
		err := t.conn.WriteMessage(websocket.TextMessage, data)
		t.mu.Unlock()
		if err != nil {
			slog.Warn("WS：写入错误", "用户名", username, "错误", err)
		}
	}
}

// SendToUser 向给定用户的所有在线连接推送一条服务端生成的消息。
func (h *WSHub) SendToUser(username string, data []byte) int {
	h.mu.RLock()
	conns, ok := h.connections[username]
	if !ok {
		h.mu.RUnlock()
		return 0
	}
	type target struct {
		conn *websocket.Conn
		mu   *sync.Mutex
	}
	targets := make([]target, 0, len(conns))
	for conn := range conns {
		targets = append(targets, target{
			conn: conn,
			mu:   h.writeLocks[conn],
		})
	}
	h.mu.RUnlock()

	sent := 0
	for _, t := range targets {
		if t.mu == nil {
			continue
		}
		t.mu.Lock()
		err := t.conn.WriteMessage(websocket.TextMessage, data)
		t.mu.Unlock()
		if err != nil {
			slog.Warn("WS：服务端推送写入错误", "用户名", username, "错误", err)
			continue
		}
		h.totalOutboundMsgs.Add(1)
		sent++
	}
	return sent
}

// HandleWebSocket 是 Fiber WebSocket 升级 handler。
// 它处理类似 STOMP 的协议流程：
//  1. Client 发送 CONNECT → server 回复 CONNECTED
//  2. Client 发送 SUBSCRIBE → server 确认
//  3. Client 发送带有剪贴板数据的 SEND → server 广播给 user 的其他 devices
func (h *WSHub) HandleWebSocket(c *websocket.Conn) {
	// 从 locals 中提取 username (在升级前的 auth 中间件设置)
	username, _ := c.Locals("username").(string)
	if username == "" {
		slog.Warn("WS：连接中缺少用户名，正在关闭")
		c.Close()
		return
	}

	// 限制单条消息大小，防止内存耗尽
	if h.readLimit > 0 {
		c.SetReadLimit(h.readLimit)
	}

	if !h.Register(username, c) {
		c.Close()
		return
	}
	defer h.Unregister(username, c)
	defer c.Close()

	subscriptionID := ""

	for {
		_, msg, err := c.ReadMessage()
		if err != nil {
			// 正常关闭或错误
			break
		}

		h.totalInboundMsgs.Add(1)

		frame, err := protocol.ParseFrame(msg)
		if err != nil {
			slog.Warn("WS：无效的 STOMP 帧", "错误", err)
			continue
		}

		switch frame.Command {
		case "CONNECT":
			// 回复 CONNECTED
			resp := protocol.ConnectedFrame("1.1")
			c.WriteMessage(websocket.TextMessage, resp.Encode())

		case "SUBSCRIBE":
			subscriptionID = frame.Get("id")
			slog.Debug("WS：已订阅", "用户名", username, "订阅ID", subscriptionID)

		case "SEND":
			// 从 STOMP body 中解析剪贴板数据（明文或 E2EE 包装）。
			var clipData protocol.ClipboardData
			if err := json.Unmarshal([]byte(frame.Body), &clipData); err != nil {
				slog.Warn("WS：无效的剪贴板数据", "错误", err)
				continue
			}

			msgType := clipData.Type
			msgSize := sizefmt.HumanSizeFromPayload(clipData.Type, clipData.Payload)

			// E2EE 开启时，body 是加密 JSON 包（nonce/ciphertext/tag），
			// 直接反序列化到 ClipboardData 会得到空 type/payload。
			if clipData.Type == "" && clipData.Payload == "" {
				msgType = "unknown"
				msgSize = sizefmt.FormatBytes(int64(len(frame.Body)))

				var envelope map[string]json.RawMessage
				if err := json.Unmarshal([]byte(frame.Body), &envelope); err == nil {
					if _, ok := envelope["ciphertext"]; ok {
						msgType = "e2ee_envelope"
					}
				}
			}

			slog.Info(
				"WS：收到剪贴板发送请求",
				"用户名", username,
				"类型", msgType,
				"体积", msgSize,
			)
			if msgType == "e2ee_envelope" {
				slog.Debug("WS：E2EE 已开启，剪贴板类型和真实内容大小需在客户端或文件上传日志中查看")
			}

			if h.automation != nil {
				event := automation.NewClipboardEvent(username, c.RemoteAddr().String(), clipData, msgType == "e2ee_envelope", frame.Body)
				decision := h.automation.Evaluate(context.Background(), event)
				for _, notice := range decision.Notices {
					h.sendNotice(c, subscriptionID, frame.Get("destination"), notice)
				}
				if decision.Outcome == automation.OutcomeBlock {
					slog.Info("WS：自动化规则阻断剪贴板广播", "用户名", username, "event_id", event.EventID)
					continue
				}
			}

			// 包装在 MESSAGE 帧中进行交付
			msgFrame := protocol.MessageFrame(
				frame.Get("destination"),
				subscriptionID,
				"", // message-id (自动)
				frame.Body,
			)
			h.Broadcast(username, c, msgFrame.Encode())

		case "DISCONNECT":
			return

		default:
			slog.Debug("WS：未知命令", "命令", frame.Command)
		}
	}
}

func (h *WSHub) sendNotice(conn *websocket.Conn, subscriptionID string, destination string, notice automation.SystemNotice) {
	if conn == nil {
		return
	}
	payload, err := json.Marshal(notice)
	if err != nil {
		return
	}
	clipData := protocol.ClipboardData{
		Type:    constants.TypeSystemNotice,
		Payload: string(payload),
	}
	body, err := clipData.Encode()
	if err != nil {
		return
	}
	msgFrame := protocol.MessageFrame(destination, subscriptionID, "", string(body))

	h.mu.RLock()
	lock := h.writeLocks[conn]
	h.mu.RUnlock()
	if lock == nil {
		return
	}
	lock.Lock()
	err = conn.WriteMessage(websocket.TextMessage, msgFrame.Encode())
	lock.Unlock()
	if err != nil {
		slog.Warn("WS：自动化通知写入错误", "错误", err)
		return
	}
	h.totalOutboundMsgs.Add(1)
}

// Stats 返回当前 hub 统计信息。
func (h *WSHub) Stats() map[string]int64 {
	h.mu.RLock()
	activeConns := int64(0)
	for _, conns := range h.connections {
		activeConns += int64(len(conns))
	}
	h.mu.RUnlock()

	return map[string]int64{
		"active_connections":  activeConns,
		"total_connections":   h.totalConnections.Load(),
		"total_inbound_msgs":  h.totalInboundMsgs.Load(),
		"total_outbound_msgs": h.totalOutboundMsgs.Load(),
	}
}

// UserConnectionCount returns the number of active WebSocket connections for one user.
func (h *WSHub) UserConnectionCount(username string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.connections[username])
}
