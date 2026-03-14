package main

import (
	"fmt"
	"os/exec"
	"syscall"
)

func showNotification(title, msg string) {
	script := fmt.Sprintf(`
[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] | Out-Null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom, ContentType = WindowsRuntime] | Out-Null
$template = [Windows.UI.Notifications.ToastNotificationManager]::GetTemplateContent([Windows.UI.Notifications.ToastTemplateType]::ToastText02)
$template.SelectSingleNode('//text[@id=1]').InnerText = '%s'
$template.SelectSingleNode('//text[@id=2]').InnerText = '%s'
$notifier = [Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('DLNA Renderer')
$notifier.Show([Windows.UI.Notifications.ToastNotification]::new($template))
`, psEscape(title), psEscape(msg))

	cmd := exec.Command("powershell",
		"-NoProfile", "-NonInteractive",
		"-WindowStyle", "Hidden",
		"-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000,
	}
	cmd.Start()
	appLog("INFO", fmt.Sprintf("系统通知: %s - %s", title, msg))
}

func psEscape(s string) string {
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
