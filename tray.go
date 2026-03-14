package main

import (
	"fmt"
	"os"
	"time"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows/registry"
)

const autoRunKey = `Software\Microsoft\Windows\CurrentVersion\Run`
const autoRunName = "DLNARenderer"

var (
	mReceiving  *systray.MenuItem
	mPause      *systray.MenuItem
	mResume     *systray.MenuItem
	mAutoStart  *systray.MenuItem
	mStatusName *systray.MenuItem
)

func onTrayReady() {
	iconData := loadIconData()
	if len(iconData) > 22 {
		systray.SetIcon(iconData)
	}
	systray.SetTitle("DLNA投屏")
	systray.SetTooltip("DLNA 投屏接收器 - " + friendlyName)

	mStatusName = systray.AddMenuItem("📺 "+friendlyName, "当前设备名称")
	mStatusName.Disable()
	systray.AddSeparator()

	mReceiving = systray.AddMenuItem("✅ 正在接收投屏", "投屏接收状态")
	mReceiving.Disable()
	mPause = systray.AddMenuItem("⏸ 暂停接收投屏", "暂停接收投屏")
	mResume = systray.AddMenuItem("▶ 继续接收投屏", "继续接收投屏")
	mResume.Hide()

	systray.AddSeparator()
	mChangeName := systray.AddMenuItem("✏ 修改设备名称", "修改显示的设备名称")
	mChangeMpv := systray.AddMenuItem("📁 修改MPV路径", "修改MPV程序路径")

	systray.AddSeparator()
	if isAutoStartEnabled() {
		mAutoStart = systray.AddMenuItem("✅ 开机自动启动（点击关闭）", "关闭开机自动启动")
	} else {
		mAutoStart = systray.AddMenuItem("🔲 开机自动启动（点击开启）", "开启开机自动启动")
	}

	systray.AddSeparator()
	mCopyURI := systray.AddMenuItem("📋 复制当前播放地址", "复制MPV正在播放的地址到剪贴板")
	mViewLog := systray.AddMenuItem("📄 查看运行日志", "查看程序运行日志")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("❌ 退出", "退出程序")

	// 每个菜单项独立 goroutine，互不阻塞
	go func() {
		for range mPause.ClickedCh {
			receiving = false
			mPause.Hide()
			mResume.Show()
			mReceiving.SetTitle("⏸ 已暂停接收投屏")
			systray.SetTooltip("DLNA 投屏接收器 - 已暂停")
			appLog("INFO", "用户暂停接收投屏")
		}
	}()

	go func() {
		for range mResume.ClickedCh {
			receiving = true
			mResume.Hide()
			mPause.Show()
			mReceiving.SetTitle("✅ 正在接收投屏")
			systray.SetTooltip("DLNA 投屏接收器 - " + friendlyName)
			appLog("INFO", "用户恢复接收投屏")
		}
	}()

	go func() {
		for range mChangeName.ClickedCh {
			newName := inputDialogUTF8("修改设备名称", "请输入新的设备名称：", friendlyName)
			if newName != "" && newName != friendlyName {
				oldName := friendlyName
				friendlyName = newName
				saveConfig()
				mStatusName.SetTitle("📺 " + friendlyName)
				systray.SetTooltip("DLNA 投屏接收器 - " + friendlyName)
				appLog("INFO", fmt.Sprintf("设备名称已从 [%s] 更新为 [%s]", oldName, newName))
				go func() {
					sendNotifyByebyes()
					time.Sleep(300 * time.Millisecond)
					sendNotifyAlives()
					appLog("INFO", "设备名称变更已广播")
				}()
			}
		}
	}()

	go func() {
		for range mChangeMpv.ClickedCh {
			newPath := inputDialogUTF8("修改MPV路径", "请输入MPV程序完整路径：", mpvPath)
			if newPath != "" && newPath != mpvPath {
				if _, err := os.Stat(newPath); os.IsNotExist(err) {
					msgBox("错误", "MPV程序不存在：\n"+newPath)
					appLog("ERROR", "MPV路径无效: "+newPath)
				} else {
					mpvPath = newPath
					saveConfig()
					mpvMu.Lock()
					if pipeFile != nil {
						pipeFile.Close()
						pipeFile = nil
					}
					mpvMu.Unlock()
					appLog("INFO", "MPV路径已更新: "+mpvPath)
					msgBox("提示", "MPV路径已更新，下次投屏时生效")
				}
			}
		}
	}()

	go func() {
		for range mAutoStart.ClickedCh {
			if isAutoStartEnabled() {
				if err := disableAutoStart(); err != nil {
					msgBox("错误", "关闭开机启动失败：\n"+err.Error())
				} else {
					mAutoStart.SetTitle("🔲 开机自动启动（点击开启）")
					appLog("INFO", "已关闭开机自动启动")
				}
			} else {
				if err := enableAutoStart(); err != nil {
					msgBox("错误", "设置开机启动失败：\n"+err.Error())
				} else {
					mAutoStart.SetTitle("✅ 开机自动启动（点击关闭）")
					appLog("INFO", "已开启开机自动启动")
				}
			}
		}
	}()

	go func() {
		for range mCopyURI.ClickedCh {
			uri := currentURI
			if uri == "" {
				msgBox("提示", "当前没有正在播放的地址")
			} else {
				if err := setClipboard(uri); err != nil {
					msgBox("错误", "复制失败："+err.Error())
				} else {
					appLog("INFO", "已复制播放地址到剪贴板: "+uri)
					msgBox("已复制", "播放地址已复制到剪贴板：\n\n"+uri)
				}
			}
		}
	}()

	go func() {
		for range mViewLog.ClickedCh {
			showLogDialog()
		}
	}()

	go func() {
		for range mQuit.ClickedCh {
			appLog("INFO", "用户退出程序")
			cleanup()
			systray.Quit()
			os.Exit(0)
		}
	}()
}

func onTrayExit() {}

func loadIconData() []byte {
	data, err := os.ReadFile("icon.ico")
	if err == nil {
		return data
	}
	return []byte{
		0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x10, 0x10,
		0x00, 0x00, 0x01, 0x00, 0x20, 0x00, 0x68, 0x04,
		0x00, 0x00, 0x16, 0x00, 0x00, 0x00,
	}
}

// ─── 开机启动 ───

func isAutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetStringValue(autoRunName)
	return err == nil && val != ""
}

func enableAutoStart() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, autoRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(autoRunName, `"`+getExePath()+`"`)
}

func disableAutoStart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.DeleteValue(autoRunName)
}

// escapePS 供 notify.go 使用
func escapePS(s string) string {
	result := ""
	for _, c := range s {
		if c == '\'' {
			result += "''"
		} else {
			result += string(c)
		}
	}
	return result
}
