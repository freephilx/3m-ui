package tui

import (
	"os"
	"strings"
)

// Strings holds every user-visible label of the console. Chinese and English
// are two complete tables - the console never mixes both into one line.
type Strings struct {
	AppTitle    string
	CatOverview string
	CatService  string
	CatCerts    string
	CatSystem   string

	Running string
	Stopped string
	Unknown string

	Version   string
	Port      string
	Listen    string
	ListenAll string
	PublicURL string
	Autostart string
	CertCount string
	Channel   string
	None      string

	ActBoot string

	Issuer   string
	Expires  string
	DaysLeft string
	NoCerts  string
	Expired  string

	ConfirmStop    string
	ConfirmRestart string
	ConfirmStart   string
	BootTitle      string
	Enable         string
	Disable        string
	Confirm        string
	Cancel         string

	Hints     string
	ModalHint string
	ExitMsg   string
	NeedTTY   string
	DumbTerm  string

	Done      string
	Failed    string
	LogsTitle string
	ErrTitle  string
	AnyKey    string
}

var zh = Strings{
	AppTitle:    "3m-ui 控制中心",
	CatOverview: "概览",
	CatService:  "服务",
	CatCerts:    "证书",
	CatSystem:   "系统",

	Running: "运行中",
	Stopped: "未运行",
	Unknown: "状态未知",

	Version:   "版本",
	Port:      "面板端口",
	Listen:    "监听地址",
	ListenAll: "全部网卡",
	PublicURL: "公开地址",
	Autostart: "开机自启",
	CertCount: "证书数量",
	Channel:   "更新通道",
	None:      "未设置",

	ActBoot: "开机自启",

	Issuer:   "颁发者",
	Expires:  "到期时间",
	DaysLeft: "剩余天数",
	NoCerts:  "未发现证书文件",
	Expired:  "已过期",

	ConfirmStop:    "确定停止面板服务吗？",
	ConfirmRestart: "确定重启面板服务吗？",
	ConfirmStart:   "确定启动面板服务吗？",
	BootTitle:      "开机自启",
	Enable:         "开启",
	Disable:        "关闭",
	Confirm:        "确认",
	Cancel:         "取消",

	Hints:     "[←→] 切栏  [↑↓] 选择  [Enter] 确认  [r] 刷新  [q] 返回",
	ModalHint: "[←→/↑↓] 选择  [Enter] 确定  [Esc] 取消",
	ExitMsg:   "已退出控制台",
	NeedTTY:   "控制台需要真实终端（TTY）才能运行",
	DumbTerm:  "当前终端不支持（TERM=dumb），无法启动控制台",

	Done:      "执行成功",
	Failed:    "执行失败",
	LogsTitle: "最近日志",
	ErrTitle:  "错误",
	AnyKey:    "按任意键返回",
}

var en = Strings{
	AppTitle:    "3m-ui Control Center",
	CatOverview: "Overview",
	CatService:  "Service",
	CatCerts:    "Certs",
	CatSystem:   "System",

	Running: "running",
	Stopped: "stopped",
	Unknown: "unknown",

	Version:   "Version",
	Port:      "Panel port",
	Listen:    "Listen",
	ListenAll: "all interfaces",
	PublicURL: "Public URL",
	Autostart: "Autostart",
	CertCount: "Certificates",
	Channel:   "Channel",
	None:      "not set",

	ActBoot: "Autostart",

	Issuer:   "Issuer",
	Expires:  "Expires",
	DaysLeft: "Days left",
	NoCerts:  "No certificate files found",
	Expired:  "expired",

	ConfirmStop:    "Stop the panel service?",
	ConfirmRestart: "Restart the panel service?",
	ConfirmStart:   "Start the panel service?",
	BootTitle:      "Autostart",
	Enable:         "Enable",
	Disable:        "Disable",
	Confirm:        "Confirm",
	Cancel:         "Cancel",

	Hints:     "[←→] pane  [↑↓] move  [Enter] select  [r] refresh  [q] back",
	ModalHint: "[←→/↑↓] choose  [Enter] ok  [Esc] cancel",
	ExitMsg:   "Console closed",
	NeedTTY:   "The console needs a real terminal (TTY)",
	DumbTerm:  "This terminal is not supported (TERM=dumb)",

	Done:      "Done",
	Failed:    "Failed",
	LogsTitle: "Recent logs",
	ErrTitle:  "Error",
	AnyKey:    "Press any key to go back",
}

// pickStrings mirrors the shell menu's precedence: THREE_M_UI_LANG wins, then
// the locale, and the project default is Chinese.
func pickStrings() Strings {
	switch os.Getenv("THREE_M_UI_LANG") {
	case "zh":
		return zh
	case "en":
		return en
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		v := strings.ToLower(os.Getenv(key))
		if strings.HasPrefix(v, "zh") {
			return zh
		}
		if strings.HasPrefix(v, "en") {
			return en
		}
	}
	return zh
}
