package tui

import (
	"os"
	"strings"
)

// Strings holds every user-visible label of the console. Chinese and English
// are two complete tables - the console never mixes both into one line.
// When adding a field here, always fill BOTH tables; the test below fails the
// build otherwise.
type Strings struct {
	// --- categories (mirror the web UI navigation) ---
	AppTitle     string
	CatDashboard string
	CatListeners string
	CatUsers     string
	CatShare     string
	CatTraffic   string
	CatCluster   string
	CatRouting   string
	CatCore      string
	CatLogs      string
	CatConfig    string
	CatSettings  string
	CatService   string
	CatCerts     string
	CatSystem    string

	// --- generic ---
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
	ActStart       string
	ActStop        string
	ActRestart     string
	ActLogs        string
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

	// --- dashboard ---
	UsersTotal   string
	UsersActive  string
	ListenersAll string
	ListenersOn  string
	TrafficAll   string
	Upload       string
	Download     string
	CoreState    string
	Uptime       string
	PanelAPI     string
	APIReachable string
	APIDown      string
	RefreshHint  string

	// --- users ---
	Username     string
	TrafficUsed  string
	TrafficLimit string
	ExpireAt     string
	Online       string
	LastSeen     string
	IPLimit      string
	State        string
	Remark       string
	SubToken     string
	NoUsers      string
	UserDetail   string

	ActEnableUser   string
	ActDisableUser  string
	ActResetTraffic string
	ActRotateToken  string
	ActDeleteUser   string

	ConfirmDisableUser  string
	ConfirmResetTraffic string
	ConfirmRotateToken  string
	ConfirmDeleteUser   string

	// --- listeners ---
	Protocol    string
	BindAddress string
	TLS         string
	UDP         string
	Multiplier  string
	Runtime     string
	NoListeners string
	ListenerDet string

	ActEnableListener  string
	ActDisableListener string
	ConfirmListenerOn  string
	ConfirmListenerOff string

	// --- share ---
	ShareHint  string
	SubURL     string
	FmtMihomo  string
	FmtV2Ray   string
	FmtSingbox string
	FmtURI     string
	FmtHTML    string
	ShareTip   string
	NoSubToken string

	// --- traffic ---
	Ranking     string
	Total       string
	LiveRate    string
	Connections string
	NoTraffic   string

	// --- cluster ---
	RemotePanel string
	BaseURL     string
	LastStatus  string
	LastCheck   string
	MirrorNodes string
	NoServers   string
	NoMirrors   string
	Node        string

	// --- routing ---
	Rules        string
	RuleProvider string
	Groups       string
	WarpGlobal   string
	WarpDomains  string
	NoRules      string
	ClientRules  string
	ServerRules  string
	DNSMode      string

	// --- core ---
	CoreVersion        string
	CorePID            string
	CoreRunning        string
	CoreLogs           string
	ActCoreStart       string
	ActCoreStop        string
	ActCoreRestart     string
	ConfirmCoreStart   string
	ConfirmCoreStop    string
	ConfirmCoreRestart string
	CoreUnsupported    string

	// --- logs ---
	PanelLogs string
	SvcLogs   string
	NoLogs    string

	// --- config ---
	ConfigFile string
	WebPath    string
	SubPort    string
	SubPath    string
	SubListen  string
	DBPath     string
	CoreBin    string

	// --- settings ---
	Telegram   string
	TgBot      string
	TgChats    string
	TgEnabled  string
	AccessProf string
	PublicHost string
	PublicPort string
	SNI        string
	ResetDay   string
	SubPage    string
	SubTitle   string
	SubEncrypt string

	// --- status / errors ---
	Offline    string
	OfflineTip string
	DBUnavail  string
	NeedRoot   string
	Copied     string
	Unlimited  string
	Never      string
}

var zh = Strings{
	AppTitle:     "3m-ui 控制中心",
	CatDashboard: "概览",
	CatListeners: "入站",
	CatUsers:     "用户",
	CatShare:     "分享",
	CatTraffic:   "流量",
	CatCluster:   "集群",
	CatRouting:   "路由",
	CatCore:      "内核",
	CatLogs:      "日志",
	CatConfig:    "配置",
	CatSettings:  "设置",
	CatService:   "服务",
	CatCerts:     "证书",
	CatSystem:    "系统",

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
	ActStart:       "启动服务",
	ActStop:        "停止服务",
	ActRestart:     "重启服务",
	ActLogs:        "查看日志",

	Hints:     "[←→] 切栏  [↑↓] 选择  [Enter] 进入/执行  [r] 刷新  [q] 返回",
	ModalHint: "[←→/↑↓] 选择  [Enter] 确定  [Esc] 取消",
	ExitMsg:   "已退出控制台",
	NeedTTY:   "控制台需要真实终端（TTY）才能运行",
	DumbTerm:  "当前终端不支持（TERM=dumb），无法启动控制台",

	Done:      "执行成功",
	Failed:    "执行失败",
	LogsTitle: "最近日志",
	ErrTitle:  "错误",
	AnyKey:    "按任意键返回",

	UsersTotal:   "用户总数",
	UsersActive:  "活跃用户",
	ListenersAll: "入站总数",
	ListenersOn:  "启用入站",
	TrafficAll:   "周期总流量",
	Upload:       "上传",
	Download:     "下载",
	CoreState:    "内核状态",
	Uptime:       "运行时长",
	PanelAPI:     "面板 API",
	APIReachable: "可连接",
	APIDown:      "未连接",
	RefreshHint:  "按 r 刷新数据",

	Username:     "用户名",
	TrafficUsed:  "已用流量",
	TrafficLimit: "流量上限",
	ExpireAt:     "到期时间",
	Online:       "在线",
	LastSeen:     "最后在线",
	IPLimit:      "IP 限制",
	State:        "状态",
	Remark:       "备注",
	SubToken:     "订阅令牌",
	NoUsers:      "暂无用户",
	UserDetail:   "用户详情",

	ActEnableUser:   "启用用户",
	ActDisableUser:  "禁用用户",
	ActResetTraffic: "重置流量",
	ActRotateToken:  "重置订阅令牌",
	ActDeleteUser:   "删除用户",

	ConfirmDisableUser:  "确定禁用该用户吗？",
	ConfirmResetTraffic: "确定清零该用户的已用流量吗？",
	ConfirmRotateToken:  "重新生成订阅令牌后，旧的订阅链接会失效。继续吗？",
	ConfirmDeleteUser:   "确定删除该用户吗？此操作不可撤销。",

	Protocol:    "协议",
	BindAddress: "绑定地址",
	TLS:         "TLS",
	UDP:         "UDP",
	Multiplier:  "流量倍率",
	Runtime:     "运行状态",
	NoListeners: "暂无入站",
	ListenerDet: "入站详情",

	ActEnableListener:  "启用入站",
	ActDisableListener: "禁用入站",
	ConfirmListenerOn:  "确定启用该入站吗？",
	ConfirmListenerOff: "确定禁用该入站吗？客户端将无法连接。",

	ShareHint:  "选中用户后回车查看订阅链接",
	SubURL:     "订阅链接",
	FmtMihomo:  "Mihomo / Clash",
	FmtV2Ray:   "v2ray（Base64）",
	FmtSingbox: "sing-box",
	FmtURI:     "链接列表",
	FmtHTML:    "订阅信息页",
	ShareTip:   "链接基于公开地址生成，供客户端导入",
	NoSubToken: "尚未生成订阅令牌",

	Ranking:     "流量排行",
	Total:       "合计",
	LiveRate:    "实时速率",
	Connections: "连接数",
	NoTraffic:   "暂无流量数据",

	RemotePanel: "远程面板",
	BaseURL:     "地址",
	LastStatus:  "最近状态",
	LastCheck:   "最近检查",
	MirrorNodes: "镜像节点",
	NoServers:   "未配置远程面板",
	NoMirrors:   "暂无镜像节点",
	Node:        "节点",

	Rules:        "规则",
	RuleProvider: "规则提供者",
	Groups:       "代理组",
	WarpGlobal:   "WARP 全局",
	WarpDomains:  "WARP 域名",
	NoRules:      "未配置规则",
	ClientRules:  "客户端路由",
	ServerRules:  "服务端路由",
	DNSMode:      "DNS 模式",

	CoreVersion: "内核版本",
	CorePID:     "进程号",
	CoreRunning: "内核运行",
	CoreLogs:    "内核日志",

	ActCoreStart:       "启动内核",
	ActCoreStop:        "停止内核",
	ActCoreRestart:     "重启内核",
	ConfirmCoreStart:   "确定启动 Mihomo 内核吗？",
	ConfirmCoreStop:    "确定停止 Mihomo 内核吗？所有节点会中断。",
	ConfirmCoreRestart: "确定重启 Mihomo 内核吗？连接会短暂中断。",
	CoreUnsupported:    "当前平台不支持内核自动更新",

	PanelLogs: "面板日志",
	SvcLogs:   "服务日志",
	NoLogs:    "暂无日志",

	ConfigFile: "配置文件",
	WebPath:    "面板路径",
	SubPort:    "订阅端口",
	SubPath:    "订阅路径",
	SubListen:  "订阅监听",
	DBPath:     "数据库",
	CoreBin:    "内核程序",

	Telegram:   "Telegram",
	TgBot:      "机器人",
	TgChats:    "会话",
	TgEnabled:  "启用状态",
	AccessProf: "分享档",
	PublicHost: "公开域名",
	PublicPort: "公开端口",
	SNI:        "SNI",
	ResetDay:   "流量重置日",
	SubPage:    "订阅页",
	SubTitle:   "订阅页标题",
	SubEncrypt: "订阅加密",

	Offline:    "面板未运行",
	OfflineTip: "面板服务未运行，以下为数据库中的静态数据",
	DBUnavail:  "数据库不可用",
	NeedRoot:   "需要 root 权限",
	Copied:     "已复制",
	Unlimited:  "不限",
	Never:      "永不过期",
}

var en = Strings{
	AppTitle:     "3m-ui Control Center",
	CatDashboard: "Dashboard",
	CatListeners: "Listeners",
	CatUsers:     "Users",
	CatShare:     "Share",
	CatTraffic:   "Traffic",
	CatCluster:   "Cluster",
	CatRouting:   "Routing",
	CatCore:      "Core",
	CatLogs:      "Logs",
	CatConfig:    "Config",
	CatSettings:  "Settings",
	CatService:   "Service",
	CatCerts:     "Certs",
	CatSystem:    "System",

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
	ActStart:       "Start service",
	ActStop:        "Stop service",
	ActRestart:     "Restart service",
	ActLogs:        "View logs",

	Hints:     "[←→] pane  [↑↓] move  [Enter] open/run  [r] refresh  [q] back",
	ModalHint: "[←→/↑↓] choose  [Enter] ok  [Esc] cancel",
	ExitMsg:   "Console closed",
	NeedTTY:   "The console needs a real terminal (TTY)",
	DumbTerm:  "This terminal is not supported (TERM=dumb)",

	Done:      "Done",
	Failed:    "Failed",
	LogsTitle: "Recent logs",
	ErrTitle:  "Error",
	AnyKey:    "Press any key to go back",

	UsersTotal:   "Users",
	UsersActive:  "Active users",
	ListenersAll: "Listeners",
	ListenersOn:  "Enabled",
	TrafficAll:   "Cycle traffic",
	Upload:       "Upload",
	Download:     "Download",
	CoreState:    "Core state",
	Uptime:       "Uptime",
	PanelAPI:     "Panel API",
	APIReachable: "reachable",
	APIDown:      "unreachable",
	RefreshHint:  "press r to refresh",

	Username:     "Username",
	TrafficUsed:  "Used",
	TrafficLimit: "Limit",
	ExpireAt:     "Expires",
	Online:       "Online",
	LastSeen:     "Last seen",
	IPLimit:      "IP limit",
	State:        "State",
	Remark:       "Remark",
	SubToken:     "Sub token",
	NoUsers:      "No users yet",
	UserDetail:   "User details",

	ActEnableUser:   "Enable user",
	ActDisableUser:  "Disable user",
	ActResetTraffic: "Reset traffic",
	ActRotateToken:  "Rotate sub token",
	ActDeleteUser:   "Delete user",

	ConfirmDisableUser:  "Disable this user?",
	ConfirmResetTraffic: "Reset this user's used traffic to zero?",
	ConfirmRotateToken:  "Rotating the subscription token invalidates the old link. Continue?",
	ConfirmDeleteUser:   "Delete this user? This cannot be undone.",

	Protocol:    "Protocol",
	BindAddress: "Bind address",
	TLS:         "TLS",
	UDP:         "UDP",
	Multiplier:  "Traffic multiplier",
	Runtime:     "Runtime",
	NoListeners: "No listeners yet",
	ListenerDet: "Listener details",

	ActEnableListener:  "Enable listener",
	ActDisableListener: "Disable listener",
	ConfirmListenerOn:  "Enable this listener?",
	ConfirmListenerOff: "Disable this listener? Clients will lose access.",

	ShareHint:  "Select a user and press Enter for subscription links",
	SubURL:     "Subscription URL",
	FmtMihomo:  "Mihomo / Clash",
	FmtV2Ray:   "v2ray (Base64)",
	FmtSingbox: "sing-box",
	FmtURI:     "URI list",
	FmtHTML:    "Info page",
	ShareTip:   "Links are built from the public URL for client import",
	NoSubToken: "No subscription token yet",

	Ranking:     "Traffic ranking",
	Total:       "Total",
	LiveRate:    "Live rate",
	Connections: "Connections",
	NoTraffic:   "No traffic data",

	RemotePanel: "Remote panel",
	BaseURL:     "Base URL",
	LastStatus:  "Last status",
	LastCheck:   "Last check",
	MirrorNodes: "Mirrored nodes",
	NoServers:   "No remote panel configured",
	NoMirrors:   "No mirrored nodes",
	Node:        "Node",

	Rules:        "Rules",
	RuleProvider: "Rule providers",
	Groups:       "Proxy groups",
	WarpGlobal:   "WARP global",
	WarpDomains:  "WARP domains",
	NoRules:      "No rules configured",
	ClientRules:  "Client routing",
	ServerRules:  "Server routing",
	DNSMode:      "DNS mode",

	CoreVersion: "Core version",
	CorePID:     "PID",
	CoreRunning: "Core running",
	CoreLogs:    "Core logs",

	ActCoreStart:       "Start core",
	ActCoreStop:        "Stop core",
	ActCoreRestart:     "Restart core",
	ConfirmCoreStart:   "Start the Mihomo core?",
	ConfirmCoreStop:    "Stop the Mihomo core? Every node goes down.",
	ConfirmCoreRestart: "Restart the Mihomo core? Connections drop briefly.",
	CoreUnsupported:    "Core auto-update is unsupported on this platform",

	PanelLogs: "Panel logs",
	SvcLogs:   "Service logs",
	NoLogs:    "No log output",

	ConfigFile: "Config file",
	WebPath:    "Web path",
	SubPort:    "Sub port",
	SubPath:    "Sub path",
	SubListen:  "Sub listen",
	DBPath:     "Database",
	CoreBin:    "Core binary",

	Telegram:   "Telegram",
	TgBot:      "Bot",
	TgChats:    "Chats",
	TgEnabled:  "Enabled",
	AccessProf: "Access profile",
	PublicHost: "Public host",
	PublicPort: "Public port",
	SNI:        "SNI",
	ResetDay:   "Traffic reset day",
	SubPage:    "Sub page",
	SubTitle:   "Sub page title",
	SubEncrypt: "Sub encryption",

	Offline:    "panel stopped",
	OfflineTip: "Panel service is stopped; showing stored data only",
	DBUnavail:  "database unavailable",
	NeedRoot:   "root privileges required",
	Copied:     "copied",
	Unlimited:  "unlimited",
	Never:      "never",
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
