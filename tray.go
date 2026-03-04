package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
	"unsafe"

	"github.com/getlantern/systray"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
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

func runTray() {
	systray.Run(onTrayReady, onTrayExit)
}

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
	mViewLog := systray.AddMenuItem("📋 查看运行日志", "查看程序运行日志")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("❌ 退出", "退出程序")

	go func() {
		for {
			select {
			case <-mPause.ClickedCh:
				receiving = false
				mPause.Hide()
				mResume.Show()
				mReceiving.SetTitle("⏸ 已暂停接收投屏")
				systray.SetTooltip("DLNA 投屏接收器 - 已暂停")
				appLog("INFO", "用户暂停接收投屏")

			case <-mResume.ClickedCh:
				receiving = true
				mResume.Hide()
				mPause.Show()
				mReceiving.SetTitle("✅ 正在接收投屏")
				systray.SetTooltip("DLNA 投屏接收器 - " + friendlyName)
				appLog("INFO", "用户恢复接收投屏")

			case <-mChangeName.ClickedCh:
				newName := inputDialogUTF8("修改设备名称", "请输入新的设备名称：", friendlyName)
				if newName != "" && newName != friendlyName {
					oldName := friendlyName
					friendlyName = newName
					saveConfig()
					mStatusName.SetTitle("📺 " + friendlyName)
					systray.SetTooltip("DLNA 投屏接收器 - " + friendlyName)
					appLog("INFO", fmt.Sprintf("设备名称已从 [%s] 更新为 [%s]，正在通知局域网...", oldName, newName))
					go func() {
						sendNotifyByebyes()
						time.Sleep(300 * time.Millisecond)
						sendNotifyAlives()
						appLog("INFO", "设备名称变更已广播，手机端刷新投屏列表即可看到新名称")
					}()
				}

			case <-mChangeMpv.ClickedCh:
				newPath := inputDialogUTF8("修改MPV路径", "请输入MPV程序完整路径：", mpvPath)
				if newPath != "" && newPath != mpvPath {
					if _, err := os.Stat(newPath); os.IsNotExist(err) {
						msgBox("错误", "MPV程序不存在：\n"+newPath)
						appLog("ERROR", "MPV路径无效: "+newPath)
					} else {
						mpvPath = newPath
						saveConfig()
						if pipeFile != nil {
							pipeFile.Close()
							pipeFile = nil
						}
						appLog("INFO", "MPV路径已更新: "+mpvPath)
						msgBox("提示", "MPV路径已更新，下次投屏时生效")
					}
				}

			case <-mAutoStart.ClickedCh:
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

			case <-mViewLog.ClickedCh:
				showLogDialog()

			case <-mQuit.ClickedCh:
				appLog("INFO", "用户退出程序")
				cleanup()
				systray.Quit()
				os.Exit(0)
			}
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

// ========== 开机启动（注册表） ==========


func isAutoStartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoRunKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetStringValue(autoRunName)
	if err != nil {
		return false
	}
	return val != ""
}

func enableAutoStart() error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, autoRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	exePath := getExePath()
	return k.SetStringValue(autoRunName, `"`+exePath+`"`)
}

func disableAutoStart() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autoRunKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.DeleteValue(autoRunName)
}

// ========== Win32 对话框 ==========

var (
	user32          = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

const (
	MB_OK       = uintptr(0x00000000)
	MB_ICONINFO = uintptr(0x00000040)
)

func msgBox(title, msg string) {
	titlePtr, _ := windows.UTF16PtrFromString(title)
	msgPtr, _ := windows.UTF16PtrFromString(msg)
	procMessageBoxW.Call(0,
		uintptr(unsafe.Pointer(msgPtr)),
		uintptr(unsafe.Pointer(titlePtr)),
		MB_OK|MB_ICONINFO)
}

// inputDialogUTF8 让 PowerShell 把输入结果以 UTF-8 写入临时文件，解决中文乱码
func inputDialogUTF8(title, prompt, defaultVal string) string {
	tmpFile := os.TempDir() + "\\dlna-input-tmp.txt"
	os.Remove(tmpFile)

	escapedTmp := strings.ReplaceAll(tmpFile, `\`, `\\`)
	script := fmt.Sprintf(`
Add-Type -AssemblyName Microsoft.VisualBasic
$r = [Microsoft.VisualBasic.Interaction]::InputBox('%s', '%s', '%s')
[System.IO.File]::WriteAllText('%s', $r, [System.Text.Encoding]::UTF8)
`, escapePS(prompt), escapePS(title), escapePS(defaultVal), escapedTmp)

	cmd := exec.Command("powershell",
		"-WindowStyle", "Hidden",
		"-NonInteractive",
		"-Command", script)
	cmd.Run()

	data, err := os.ReadFile(tmpFile)
	os.Remove(tmpFile)
	if err != nil {
		return ""
	}
	// 去掉 UTF-8 BOM（如果有）
	result := strings.TrimSpace(string(bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})))
	return result
}

// gbkToUTF8 备用：将 GBK 字节转为 UTF-8 字符串
func gbkToUTF8(b []byte) string {
	decoder := simplifiedchinese.GBK.NewDecoder()
	reader := transform.NewReader(bytes.NewReader(b), decoder)
	result, err := io.ReadAll(reader)
	if err != nil {
		return string(b)
	}
	return string(result)
}

func showLogDialog() {
	logText := getLogText()
	tmpFile := os.TempDir() + "\\dlna-renderer-log.txt"
	os.WriteFile(tmpFile, []byte(logText), 0644)

	escapedTmp := strings.ReplaceAll(tmpFile, `\`, `\\`)
	script := fmt.Sprintf(`
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$f = New-Object System.Windows.Forms.Form
$f.Text = 'DLNA投屏接收器 - 运行日志'
$f.Size = New-Object System.Drawing.Size(820,600)
$f.StartPosition = 'CenterScreen'
$f.TopMost = $true
$tb = New-Object System.Windows.Forms.RichTextBox
$tb.Dock = 'Fill'
$tb.ReadOnly = $true
$tb.Font = New-Object System.Drawing.Font('Consolas',9)
$logpath = '%s'
$tb.Text = [System.IO.File]::ReadAllText($logpath, [System.Text.Encoding]::UTF8)
$tb.SelectionStart = $tb.Text.Length
$tb.ScrollToCaret()
$panel = New-Object System.Windows.Forms.Panel
$panel.Dock = 'Bottom'
$panel.Height = 35
$btn = New-Object System.Windows.Forms.Button
$btn.Text = '刷新日志'
$btn.Width = 100
$btn.Height = 28
$btn.Left = 5
$btn.Top = 3
$btn.Add_Click({
    $tb.Text = [System.IO.File]::ReadAllText($logpath, [System.Text.Encoding]::UTF8)
    $tb.SelectionStart = $tb.Text.Length
    $tb.ScrollToCaret()
})
$panel.Controls.Add($btn)
$f.Controls.Add($tb)
$f.Controls.Add($panel)
[void]$f.ShowDialog()
`, escapedTmp)

	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command", script)
	cmd.Start()
}
