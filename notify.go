package main

import (
	"fmt"
	"os/exec"
)

func showNotification(title, msg string) {
	// 使用 PowerShell 显示 Windows Toast 通知
	script := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom, ContentType = WindowsRuntime] | Out-Null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$template.SelectSingleNode('//text[@id=1]').InnerText = '%s'
$template.SelectSingleNode('//text[@id=2]').InnerText = '%s'
$notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('DLNA Renderer')
$notifier.Show([Windows.UI.Notifications.ToastNotification]::new($template))
`, escapePS(title), escapePS(msg))

	cmd := exec.Command("powershell", "-WindowStyle", "Hidden", "-Command", script)
	cmd.Run()
	appLog("INFO", fmt.Sprintf("系统通知: %s - %s", title, msg))
}

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
