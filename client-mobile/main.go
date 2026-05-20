package main

import (
	"context"
	"fmt"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/clipcascade/pkg/netutil"
	"github.com/grandcat/zeroconf"
)

// Main application entry point for the pure Go Fyne mobile client.
func main() {
	a := app.NewWithID("com.clipcascade.mobile")
	w := a.NewWindow("ClipCascade")
	// On mobile, Window.Resize is ignored since apps are forcibly fullscreen,
	// but we set a logical starting size for desktop debugging.
	w.Resize(fyne.NewSize(380, 600))

	// The session manages the connection to the backend Engine
	sess := NewSession(a, w)

	// Create UI elements
	title := widget.NewLabelWithStyle("ClipCascade", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})

	serverEntry := widget.NewSelectEntry(nil)
	serverEntry.SetPlaceHolder("Enter server URL or choose discovered host")
	serverURL := normalizeServerURL(a.Preferences().StringWithFallback("ServerURL", ""))
	serverEntry.Text = serverURL

	userEntry := widget.NewEntry()
	userEntry.SetPlaceHolder("Username")
	userEntry.Text = a.Preferences().StringWithFallback("Username", "")

	passEntry := widget.NewPasswordEntry()
	passEntry.SetPlaceHolder("Password")
	passEntry.Text = a.Preferences().StringWithFallback("Password", "")

	e2eCheck := widget.NewCheck("Enable E2EE Encryption", nil)
	e2eCheck.Checked = a.Preferences().BoolWithFallback("E2EE", true)

	statusLabel := widget.NewLabel("Status: Disconnected")
	statusDetailLabel := widget.NewLabel("Waiting for connection")
	statusDetailLabel.Wrapping = fyne.TextWrapWord
	onlineDevicesLabel := widget.NewLabelWithStyle("--", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	lastSyncLabel := widget.NewLabelWithStyle("--", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	historyTotalLabel := widget.NewLabelWithStyle("0", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	lastEventLabel := widget.NewLabel("最近事件：暂无")
	lastEventLabel.Wrapping = fyne.TextWrapWord
	historyCountLabel := widget.NewLabel("0 条")

	// 自动通过 mDNS 发现局域网服务器（收集全部候选，不只第一个）。
	var discoveredMu sync.Mutex
	discoveredSet := make(map[string]struct{})
	discoveredHosts := make([]string, 0, 8)
	addDiscoveredHost := func(addr string) {
		addr = strings.TrimSpace(addr)
		if addr == "" {
			return
		}
		discoveredMu.Lock()
		if _, exists := discoveredSet[addr]; exists {
			discoveredMu.Unlock()
			return
		}
		discoveredSet[addr] = struct{}{}
		discoveredHosts = append(discoveredHosts, addr)
		sort.Strings(discoveredHosts)
		options := append([]string(nil), discoveredHosts...)
		discoveredMu.Unlock()

		fyne.Do(func() {
			serverEntry.SetOptions(options)
			current := strings.TrimSpace(serverEntry.Text)
			if current == "" || current == "http://localhost:8080" {
				serverEntry.SetText(options[0])
			}
		})
	}
	// 把已保存的地址也放进下拉候选里，便于快速切换。
	addDiscoveredHost(serverURL)

	go func() {
		resolver, err := zeroconf.NewResolver(zeroconf.SelectIfaces(netutil.UsableLANInterfaces()))
		if err != nil {
			return
		}
		entries := make(chan *zeroconf.ServiceEntry)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := resolver.Browse(ctx, "_clipcascade._tcp", "local.", entries); err != nil {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case entry := <-entries:
				if entry == nil {
					continue
				}
				for _, ip := range entry.AddrIPv4 {
					if !netutil.IsUsableLANIPv4(ip) {
						continue
					}
					addDiscoveredHost(fmt.Sprintf("http://%s:%d", ip, entry.Port))
				}
				if len(entry.AddrIPv4) == 0 && entry.HostName != "" {
					host := strings.TrimSuffix(entry.HostName, ".")
					addDiscoveredHost(fmt.Sprintf("http://%s:%d", host, entry.Port))
				}
			}
		}
	}()

	var connectBtn, disconnectBtn *widget.Button
	var refreshDeviceStatus func()

	applyConnectionState := func(state string) {
		switch state {
		case "connecting":
			statusLabel.SetText("连接中")
			statusDetailLabel.SetText("正在连接服务器...")
			onlineDevicesLabel.SetText("--")
			connectBtn.Disable()
			disconnectBtn.Disable()
		case "connected":
			statusLabel.SetText("已连接")
			statusDetailLabel.SetText("实时同步已启动")
			connectBtn.Disable()
			disconnectBtn.Enable()
			if refreshDeviceStatus != nil {
				go refreshDeviceStatus()
			}
		case "disconnecting":
			statusLabel.SetText("断开中")
			statusDetailLabel.SetText("正在关闭连接...")
			connectBtn.Disable()
			disconnectBtn.Disable()
		case "reconnecting":
			statusLabel.SetText("重连中")
			statusDetailLabel.SetText("连接中断，正在自动重连")
			connectBtn.Disable()
			disconnectBtn.Disable()
		case "error":
			statusLabel.SetText("连接失败")
			statusDetailLabel.SetText("请检查服务器地址、账号或网络")
			onlineDevicesLabel.SetText("--")
			connectBtn.Enable()
			disconnectBtn.Disable()
		default:
			statusLabel.SetText("未连接")
			statusDetailLabel.SetText("保存配置后连接服务器")
			onlineDevicesLabel.SetText("--")
			connectBtn.Enable()
			disconnectBtn.Disable()
		}
	}

	connectBtn = widget.NewButtonWithIcon("连接", theme.LoginIcon(), func() {
		currentState := sess.Status()
		if currentState == "connecting" || currentState == "connected" || currentState == "reconnecting" {
			return
		}
		serverURL := normalizeServerURL(serverEntry.Text)
		if serverURL == "" || userEntry.Text == "" {
			dialog.ShowInformation("Error", "Please enter server URL and credentials", w)
			return
		}
		serverEntry.SetText(serverURL)

		// Save preferences
		a.Preferences().SetString("ServerURL", serverURL)
		a.Preferences().SetString("Username", userEntry.Text)
		a.Preferences().SetString("Password", passEntry.Text)
		a.Preferences().SetBool("E2EE", e2eCheck.Checked)

		go func() {
			err := sess.Connect(serverURL, userEntry.Text, passEntry.Text, e2eCheck.Checked)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, w)
				}
			})
		}()
	})

	disconnectBtn = widget.NewButtonWithIcon("断开", theme.LogoutIcon(), func() {
		currentState := sess.Status()
		if currentState != "connected" && currentState != "reconnecting" {
			return
		}
		go sess.Disconnect()
	})

	type historyItem struct {
		Text      string
		Direction string
		At        time.Time
	}

	historyLimit := 20
	var historyMu sync.Mutex
	historyItems := make([]historyItem, 0, 20)

	historyList := widget.NewList(
		func() int {
			historyMu.Lock()
			defer historyMu.Unlock()
			if len(historyItems) == 0 {
				return 1
			}
			return len(historyItems)
		},
		func() fyne.CanvasObject {
			meta := widget.NewLabel("·")
			meta.Wrapping = fyne.TextWrapOff
			txt := widget.NewLabel("暂无历史记录")
			txt.Wrapping = fyne.TextWrapWord
			return container.NewBorder(nil, nil, meta, nil, txt)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			row := obj.(*fyne.Container)
			meta := row.Objects[1].(*widget.Label)
			txt := row.Objects[0].(*widget.Label)

			historyMu.Lock()
			defer historyMu.Unlock()

			if len(historyItems) == 0 {
				meta.SetText("·")
				txt.SetText("暂无历史记录")
				return
			}
			item := historyItems[id]
			arrow := "接收"
			if item.Direction == "sent" {
				arrow = "发送"
			}
			meta.SetText(fmt.Sprintf("%s\n%s", arrow, item.At.Format("15:04")))
			txt.SetText(item.Text)
		},
	)
	historyScroll := container.NewVScroll(historyList)
	historyScroll.SetMinSize(fyne.NewSize(0, 240))
	historyList.OnSelected = func(id widget.ListItemID) {
		historyMu.Lock()
		if len(historyItems) == 0 || id < 0 || id >= len(historyItems) {
			historyMu.Unlock()
			historyList.Unselect(id)
			return
		}
		text := historyItems[id].Text
		historyMu.Unlock()

		w.Clipboard().SetContent(text)
		a.SendNotification(fyne.NewNotification("ClipCascade", "历史内容已写入本机剪贴板"))
		historyList.Unselect(id)
	}

	appendHistory := func(text, direction string) {
		text = strings.TrimSpace(text)
		if text == "" {
			return
		}
		if len(text) > 160 {
			text = text[:160] + "..."
		}
		line := strings.ReplaceAll(text, "\n", " ")

		historyMu.Lock()
		if len(historyItems) > 0 && historyItems[0].Text == line && historyItems[0].Direction == direction {
			historyMu.Unlock()
			return
		}
		historyItems = append([]historyItem{{
			Text:      line,
			Direction: direction,
			At:        time.Now(),
		}}, historyItems...)
		if len(historyItems) > historyLimit {
			historyItems = historyItems[:historyLimit]
		}
		count := len(historyItems)
		latestAt := historyItems[0].At.Format("15:04:05")
		historyMu.Unlock()
		fyne.Do(func() {
			historyCountLabel.SetText(fmt.Sprintf("%d 条", count))
			historyTotalLabel.SetText(fmt.Sprintf("%d", count))
			lastSyncLabel.SetText(latestAt)
			if direction == "sent" {
				lastEventLabel.SetText("最近事件：发送文本 " + latestAt)
			} else {
				lastEventLabel.SetText("最近事件：接收文本 " + latestAt)
			}
			historyList.Refresh()
		})
	}

	applyLimit := func(n int) {
		historyMu.Lock()
		historyLimit = n
		if len(historyItems) > historyLimit {
			historyItems = historyItems[:historyLimit]
		}
		historyMu.Unlock()
		fyne.Do(func() {
			historyList.Refresh()
		})
	}
	limit10Btn := widget.NewButton("10", func() { applyLimit(10) })
	limit20Btn := widget.NewButton("20", func() { applyLimit(20) })
	clearBtn := widget.NewButton("清空", func() {
		historyMu.Lock()
		historyItems = historyItems[:0]
		historyMu.Unlock()
		fyne.Do(func() {
			historyCountLabel.SetText("0 条")
			historyTotalLabel.SetText("0")
			lastEventLabel.SetText("最近事件：暂无")
			lastSyncLabel.SetText("--")
			historyList.Refresh()
		})
	})

	sess.SetTextListener(func(text, direction string) {
		appendHistory(text, direction)
	})

	titleContainer := container.NewCenter(title)
	refreshBtn := widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), func() {
		if refreshDeviceStatus != nil {
			go refreshDeviceStatus()
		}
	})
	refreshDeviceStatus = func() {
		if !sess.IsConnected() {
			fyne.Do(func() {
				onlineDevicesLabel.SetText("--")
			})
			return
		}
		counts, err := sess.DeviceCounts()
		fyne.Do(func() {
			if err != nil {
				onlineDevicesLabel.SetText("获取失败")
				statusDetailLabel.SetText("已连接；无法获取在线设备数，请确认 server 已更新并重启")
				return
			}
			onlineDevicesLabel.SetText(fmt.Sprintf("%d", counts.OnlineDevices))
			statusDetailLabel.SetText("实时同步已启动")
		})
	}

	metric := func(title string, value *widget.Label, hint string) fyne.CanvasObject {
		caption := widget.NewLabelWithStyle(title, fyne.TextAlignCenter, fyne.TextStyle{})
		sub := widget.NewLabelWithStyle(hint, fyne.TextAlignCenter, fyne.TextStyle{})
		sub.Wrapping = fyne.TextWrapWord
		return container.NewVBox(caption, value, sub)
	}

	// 使用 Grid 确保输入框可以随屏幕宽度自动伸缩拉伸 (特别是在高分辨率及异形屏手机上)
	configCard := widget.NewCard("连接配置", "", container.NewGridWithColumns(1,
		serverEntry,
		userEntry,
		passEntry,
		e2eCheck,
	))

	connCard := widget.NewCard("运行状态", "", container.NewVBox(
		statusLabel,
		statusDetailLabel,
		container.NewGridWithColumns(3,
			metric("在线设备", onlineDevicesLabel, "同账号连接数"),
			metric("历史记录", historyTotalLabel, "本地保留"),
			metric("最近同步", lastSyncLabel, "本地记录"),
		),
		lastEventLabel,
		container.NewGridWithColumns(3, connectBtn, disconnectBtn, refreshBtn),
	))

	historyHeader := container.NewBorder(
		nil,
		nil,
		container.NewHBox(widget.NewLabel("显示"), limit10Btn, limit20Btn),
		container.NewHBox(historyCountLabel, clearBtn),
		widget.NewLabel(""),
	)

	historyCard := widget.NewCard("历史记录", "点击某条可回填到本机剪贴板", container.NewBorder(
		historyHeader,
		nil, nil, nil,
		historyScroll,
	))

	// 使用 VBox 组合所有的 Card 组件，并用 Padded 增加呼吸感和边距留白
	formContent := container.NewPadded(container.NewVBox(
		titleContainer,
		connCard,
		configCard,
		historyCard,
	))

	w.SetContent(container.NewVScroll(formContent))

	sess.SetStatusListener(func(state string) {
		fyne.Do(func() {
			applyConnectionState(state)
		})
	})

	// 本机剪贴板监听：连接中自动发送，并作为“sent”进入历史记录。
	lastObservedClipboard := w.Clipboard().Content()
	go func() {
		ticker := time.NewTicker(700 * time.Millisecond)
		defer ticker.Stop()
		for range ticker.C {
			content := strings.TrimSpace(w.Clipboard().Content())
			if content == "" || content == lastObservedClipboard {
				continue
			}
			lastObservedClipboard = content
			if !sess.IsConnected() {
				continue
			}
			if content == sess.LastCopied() {
				continue
			}
			sess.SendText(content)
		}
	}()

	// 仅在移动端启用前台回到应用时的补偿同步，不在后台主动断开连接。
	// 目标是尽量保持长连；是否被系统回收由平台策略决定。
	if runtime.GOOS == "android" || runtime.GOOS == "ios" {
		a.Lifecycle().SetOnEnteredForeground(func() {
			// When app comes to foreground, we could auto-sync if connected
			if sess.IsConnected() {
				content := w.Clipboard().Content()
				if content != "" && content != sess.LastCopied() {
					sess.SendText(content)
				}
			}
		})
	}

	w.ShowAndRun()
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
