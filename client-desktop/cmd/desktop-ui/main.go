package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	desktopapp "github.com/clipcascade/desktop/app"
	"github.com/clipcascade/desktop/config"
	"github.com/clipcascade/desktop/transfer"
	"github.com/clipcascade/pkg/constants"
)

var Version = "1.15"

func main() {
	serverURL := flag.String("server", "", "Server URL")
	username := flag.String("username", "", "Login username")
	password := flag.String("password", "", "Login password")
	noE2EE := flag.Bool("no-e2ee", false, "Disable end-to-end encryption")
	saveConfig := flag.Bool("save", false, "Save provided flags to config file")
	debugLog := flag.Bool("debug", false, "Enable debug logging")
	flag.Parse()

	level := slog.LevelInfo
	if *debugLog {
		level = slog.LevelDebug
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level})))

	cfg := config.Load()
	if *serverURL != "" {
		cfg.ServerURL = config.NormalizeServerURL(*serverURL)
	}
	if *username != "" {
		cfg.Username = *username
	}
	if *password != "" {
		cfg.Password = *password
	}
	if *noE2EE {
		cfg.E2EEEnabled = false
	}
	if *saveConfig {
		if err := cfg.Save(); err != nil {
			slog.Error("failed to save config", "error", err)
			os.Exit(1)
		}
	}

	uiApp := fyneapp.NewWithID("com.clipcascade.desktopui")
	uiApp.Settings().SetTheme(theme.DefaultTheme())
	w := uiApp.NewWindow("ClipCascade")
	w.Resize(fyne.NewSize(920, 720))

	client := desktopapp.NewWindowApplication(cfg)
	if err := client.Start(true); err != nil {
		dialog.ShowError(err, w)
	}

	var deferredFiles []transfer.DeferredFileRecord
	var historyItems []desktopapp.HistoryItem
	var transferItems []transfer.Task

	title := widget.NewLabelWithStyle("ClipCascade", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	subtitle := widget.NewLabel(fmt.Sprintf("跨设备剪贴板同步 · v%s", Version))
	subtitle.Importance = widget.LowImportance

	statusValue := widget.NewLabelWithStyle("Disconnected", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	statusDetail := widget.NewLabel("未连接")
	statusDetail.Wrapping = fyne.TextWrapWord
	statusTime := widget.NewLabel("更新时间：暂无")
	statusTime.Importance = widget.LowImportance
	deviceCount := widget.NewLabel("在线设备：暂无")
	deviceCount.Importance = widget.LowImportance
	lastEvent := widget.NewLabel("最近事件：暂无同步记录")
	lastEvent.Wrapping = fyne.TextWrapWord

	serverEntry := widget.NewSelectEntry(nil)
	serverEntry.SetPlaceHolder("服务器地址，例如 http://192.168.1.8:8080")
	serverEntry.SetText(cfg.ServerURL)
	userEntry := widget.NewEntry()
	userEntry.SetPlaceHolder("用户名")
	userEntry.SetText(cfg.Username)
	passEntry := widget.NewPasswordEntry()
	passEntry.SetPlaceHolder("密码")
	passEntry.SetText(cfg.Password)
	e2eeCheck := widget.NewCheck("启用端到端加密", nil)
	e2eeCheck.SetChecked(cfg.E2EEEnabled)
	p2pCheck := widget.NewCheck("启用 P2P 辅助通道", nil)
	p2pCheck.SetChecked(cfg.P2PEnabled)
	autoReconnectCheck := widget.NewCheck("自动重连", nil)
	autoReconnectCheck.SetChecked(cfg.AutoReconnect)
	stunEntry := widget.NewEntry()
	stunEntry.SetPlaceHolder(constants.DefaultStunURL)
	stunEntry.SetText(cfg.StunURL)

	saveCurrentConfig := func() error {
		return client.SaveConfig(
			serverEntry.Text,
			userEntry.Text,
			passEntry.Text,
			e2eeCheck.Checked,
			p2pCheck.Checked,
			autoReconnectCheck.Checked,
			stunEntry.Text,
		)
	}

	refreshDeferred := func(showError bool) {
		go func() {
			items, err := client.ListDeferredFiles()
			fyne.Do(func() {
				if err != nil {
					if showError {
						dialog.ShowError(err, w)
					}
					return
				}
				deferredFiles = items
			})
		}()
	}

	discoverBtn := widget.NewButtonWithIcon("发现", theme.SearchIcon(), func() {
		go func() {
			urls, err := client.DiscoverServers()
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, w)
					return
				}
				sort.Strings(urls)
				serverEntry.SetOptions(urls)
				if strings.TrimSpace(serverEntry.Text) == "" && len(urls) > 0 {
					serverEntry.SetText(urls[0])
				}
			})
		}()
	})

	saveBtn := widget.NewButtonWithIcon("保存配置", theme.DocumentSaveIcon(), func() {
		if err := saveCurrentConfig(); err != nil {
			dialog.ShowError(err, w)
			return
		}
		dialog.ShowInformation("ClipCascade", "配置已保存", w)
	})
	connectBtn := widget.NewButtonWithIcon("连接", theme.LoginIcon(), func() {
		if err := saveCurrentConfig(); err != nil {
			dialog.ShowError(err, w)
			return
		}
		go client.Connect()
	})
	disconnectBtn := widget.NewButtonWithIcon("断开", theme.LogoutIcon(), func() {
		go client.Disconnect()
	})
	refreshBtn := widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), func() {
		go func() {
			if _, err := client.RefreshDeviceCounts(); err != nil {
				fyne.Do(func() { dialog.ShowError(err, w) })
			}
		}()
		refreshDeferred(true)
	})

	historyList := widget.NewList(
		func() int { return len(historyItems) },
		func() fyne.CanvasObject {
			icon := widget.NewIcon(theme.ContentCopyIcon())
			meta := widget.NewLabel("方向 · 时间")
			meta.Importance = widget.LowImportance
			body := widget.NewLabel("内容")
			body.Wrapping = fyne.TextWrapWord
			return container.NewBorder(nil, nil, icon, nil, container.NewVBox(meta, body))
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(historyItems) {
				return
			}
			row := obj.(*fyne.Container)
			icon := row.Objects[1].(*widget.Icon)
			box := row.Objects[0].(*fyne.Container)
			meta := box.Objects[0].(*widget.Label)
			body := box.Objects[1].(*widget.Label)
			item := historyItems[id]
			icon.SetResource(historyIcon(item.Type))
			meta.SetText(fmt.Sprintf("%s · %s", directionLabel(item.Direction), item.CreatedAt.Format("15:04:05")))
			body.SetText(historyBody(item))
		},
	)
	historyList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(historyItems) {
			return
		}
		item := historyItems[id]
		historyList.Unselect(id)
		if item.Type == constants.TypeImage && item.ImagePath != "" {
			showImagePreview(w, item.ImagePath)
			return
		}
		if item.Type == constants.TypeText && strings.TrimSpace(item.Text) != "" {
			w.Clipboard().SetContent(item.Text)
			dialog.ShowInformation("ClipCascade", "历史文本已写入本机剪贴板", w)
		}
	}

	clearHistoryBtn := widget.NewButtonWithIcon("清空", theme.DeleteIcon(), func() {
		client.ClearHistory()
	})
	historyPane := container.NewBorder(
		container.NewHBox(widget.NewLabel("剪贴板历史"), layoutSpacer(), clearHistoryBtn),
		nil, nil, nil,
		historyList,
	)

	filesList := widget.NewList(
		func() int { return len(deferredFiles) },
		func() fyne.CanvasObject {
			icon := widget.NewIcon(theme.FileIcon())
			meta := widget.NewLabel("状态")
			meta.Importance = widget.LowImportance
			name := widget.NewLabel("文件")
			name.Wrapping = fyne.TextWrapWord
			return container.NewBorder(nil, nil, icon, nil, container.NewVBox(meta, name))
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(deferredFiles) {
				return
			}
			row := obj.(*fyne.Container)
			box := row.Objects[0].(*fyne.Container)
			meta := box.Objects[0].(*widget.Label)
			name := box.Objects[1].(*widget.Label)
			item := deferredFiles[id]
			meta.SetText(fmt.Sprintf("%s · %s", deferredStatus(item), item.FileSizeHuman))
			name.SetText(item.FileName)
		},
	)
	filesList.OnSelected = func(id widget.ListItemID) {
		if id < 0 || id >= len(deferredFiles) {
			return
		}
		item := deferredFiles[id]
		filesList.Unselect(id)
		go func() {
			if item.Status == "ready" && item.RelayID != "" {
				path, err := client.DownloadDeferredFile(item)
				fyne.Do(func() {
					if err != nil {
						dialog.ShowError(err, w)
						return
					}
					dialog.ShowInformation("ClipCascade", "文件已下载并写入系统文件剪贴板：\n"+path, w)
				})
				return
			}
			_, active, err := client.RequestDeferredFile(item.DeferredID)
			fyne.Do(func() {
				if err != nil {
					dialog.ShowError(err, w)
					return
				}
				dialog.ShowInformation("ClipCascade", fmt.Sprintf("已请求源设备上传，在线源设备：%d", active), w)
				refreshDeferred(false)
			})
		}()
	}
	filesPane := container.NewBorder(
		container.NewHBox(widget.NewLabel("大文件"), layoutSpacer(), widget.NewButtonWithIcon("刷新", theme.ViewRefreshIcon(), func() { refreshDeferred(true) })),
		nil, nil, nil,
		filesList,
	)

	transfersList := widget.NewList(
		func() int { return len(transferItems) },
		func() fyne.CanvasObject {
			label := widget.NewLabel("任务")
			bar := widget.NewProgressBar()
			return container.NewVBox(label, bar)
		},
		func(id widget.ListItemID, obj fyne.CanvasObject) {
			if id < 0 || id >= len(transferItems) {
				return
			}
			box := obj.(*fyne.Container)
			label := box.Objects[0].(*widget.Label)
			bar := box.Objects[1].(*widget.ProgressBar)
			task := transferItems[id]
			label.SetText(fmt.Sprintf("%s · %s · %s", task.Direction, task.Status, task.FileName))
			if task.TotalBytes > 0 {
				bar.SetValue(float64(task.DoneBytes) / float64(task.TotalBytes))
			} else {
				bar.SetValue(0)
			}
		},
	)

	statusCard := widget.NewCard("状态", "实时连接状态和最近同步事件", container.NewVBox(
		statusValue,
		statusDetail,
		statusTime,
		deviceCount,
		lastEvent,
		container.NewGridWithColumns(3, connectBtn, disconnectBtn, refreshBtn),
	))

	connectionCard := widget.NewCard("连接", "保存服务端和账号后启动同步", container.NewVBox(
		container.NewBorder(nil, nil, nil, discoverBtn, serverEntry),
		userEntry,
		passEntry,
		container.NewGridWithColumns(3, e2eeCheck, p2pCheck, autoReconnectCheck),
		stunEntry,
		container.NewGridWithColumns(2, saveBtn, widget.NewButtonWithIcon("重连", theme.ViewRefreshIcon(), func() {
			if err := saveCurrentConfig(); err != nil {
				dialog.ShowError(err, w)
				return
			}
			go func() {
				client.Disconnect()
				time.Sleep(300 * time.Millisecond)
				client.Connect()
			}()
		})),
	))

	diagnostics := widget.NewCard("桌面诊断", "桌面端运行环境和本地路径", container.NewVBox(
		widget.NewLabel("剪贴板监听：已启动"),
		widget.NewLabel("通知：系统通知通道"),
		widget.NewLabel("配置路径："+cfg.FilePath),
		widget.NewLabel("临时文件："+filepath.Join(os.TempDir(), "ClipCascade")),
	))

	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon("剪贴板", theme.ContentCopyIcon(), historyPane),
		container.NewTabItemWithIcon("大文件", theme.FileIcon(), filesPane),
		container.NewTabItemWithIcon("传输", theme.UploadIcon(), transfersList),
	)
	tabs.SetTabLocation(container.TabLocationTop)

	content := container.NewBorder(
		container.NewVBox(title, subtitle),
		nil, nil, nil,
		container.NewVScroll(container.NewVBox(statusCard, connectionCard, diagnostics, widget.NewCard("", "剪贴板和大文件分开查看", tabs))),
	)
	w.SetContent(content)

	installTray(uiApp, w, client)
	w.SetCloseIntercept(func() { w.Hide() })

	stop := make(chan struct{})
	go func() {
		deviceTicker := time.NewTicker(5 * time.Second)
		refreshTicker := time.NewTicker(800 * time.Millisecond)
		deferredTicker := time.NewTicker(8 * time.Second)
		defer deviceTicker.Stop()
		defer refreshTicker.Stop()
		defer deferredTicker.Stop()
		refreshDeferred(false)
		for {
			select {
			case <-stop:
				return
			case <-deviceTicker.C:
				_, _ = client.RefreshDeviceCounts()
			case <-deferredTicker.C:
				refreshDeferred(false)
			case <-refreshTicker.C:
				snap := client.Snapshot()
				fyne.Do(func() {
					statusValue.SetText(statusLabel(snap.Status))
					statusDetail.SetText(snap.StatusDetail)
					if !snap.StatusAt.IsZero() {
						statusTime.SetText("更新时间：" + snap.StatusAt.Format("15:04:05"))
					}
					if !snap.Devices.UpdatedAt.IsZero() {
						deviceCount.SetText(fmt.Sprintf("在线设备：%d 台；本次复制会同步给 %d 台", snap.Devices.OnlineDevices, snap.Devices.SyncTargets))
					}
					lastEvent.SetText(snap.LastEvent)
					historyItems = snap.History
					transferItems = snap.Transfers
					historyList.Refresh()
					filesList.Refresh()
					transfersList.Refresh()
					if snap.Connected {
						connectBtn.Disable()
						disconnectBtn.Enable()
					} else {
						connectBtn.Enable()
						disconnectBtn.Disable()
					}
				})
			}
		}
	}()

	w.ShowAndRun()
	close(stop)
	client.Shutdown()
}

func installTray(a fyne.App, w fyne.Window, client *desktopapp.Application) {
	desk, ok := a.(desktop.App)
	if !ok {
		return
	}
	desk.SetSystemTrayWindow(w)
	desk.SetSystemTrayIcon(theme.ComputerIcon())
	desk.SetSystemTrayMenu(fyne.NewMenu("ClipCascade",
		fyne.NewMenuItem("显示窗口", func() { w.Show() }),
		fyne.NewMenuItem("连接", func() { go client.Connect() }),
		fyne.NewMenuItem("断开", func() { go client.Disconnect() }),
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("退出", func() {
			client.Shutdown()
			a.Quit()
		}),
	))
}

func layoutSpacer() fyne.CanvasObject {
	return layout.NewSpacer()
}

func historyIcon(t string) fyne.Resource {
	switch t {
	case constants.TypeImage:
		return theme.MediaPhotoIcon()
	case constants.TypeFileEager, constants.TypeFileStub, constants.TypeFileOffer, constants.TypeFiles:
		return theme.FileIcon()
	default:
		return theme.ContentCopyIcon()
	}
}

func historyBody(item desktopapp.HistoryItem) string {
	if item.DisplaySize != "" {
		return fmt.Sprintf("%s · %s", item.Text, item.DisplaySize)
	}
	return item.Text
}

func directionLabel(direction string) string {
	if direction == "sent" {
		return "发送"
	}
	return "接收"
}

func deferredStatus(item transfer.DeferredFileRecord) string {
	switch item.Status {
	case "pending":
		return "待请求"
	case "requested":
		return "已请求"
	case "uploading":
		if item.UploadPercent > 0 {
			return fmt.Sprintf("上传中 %d%%", item.UploadPercent)
		}
		return "上传中"
	case "ready":
		return "可下载"
	case "failed":
		if item.LastError != "" {
			return "失败：" + item.LastError
		}
		return "失败"
	default:
		return item.Status
	}
}

func statusLabel(status string) string {
	switch status {
	case "Connected ✓":
		return "已连接"
	case "Connecting...":
		return "连接中"
	case "Reconnecting...":
		return "重连中"
	case "Login Failed":
		return "连接失败"
	case "Disconnected":
		return "未连接"
	default:
		if strings.TrimSpace(status) == "" {
			return "未连接"
		}
		return status
	}
}

func showImagePreview(w fyne.Window, path string) {
	if _, err := os.Stat(path); err != nil {
		dialog.ShowError(fmt.Errorf("图片文件不存在：%s", path), w)
		return
	}
	img := canvas.NewImageFromFile(path)
	img.FillMode = canvas.ImageFillContain
	img.SetMinSize(fyne.NewSize(720, 520))
	dialog.ShowCustom("图片预览", "关闭", img, w)
}
