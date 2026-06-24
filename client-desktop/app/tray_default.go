//go:build !desktopui

package app

import "github.com/clipcascade/desktop/ui"

func newTrayController() trayController {
	return ui.NewTray()
}
