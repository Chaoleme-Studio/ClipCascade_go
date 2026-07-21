package automation

import "time"

const (
	// OutcomeAllow 表示剪贴板事件继续走正常 relay 链路。
	OutcomeAllow = "allow"
	// OutcomeBlock 表示在 enforce 模式下阻断剪贴板事件。
	OutcomeBlock = "block"

	// ActionAudit 只记录匹配事件，不调度额外任务。
	ActionAudit = "audit"
	// ActionEnqueue 创建待处理自动化任务，供外部 worker 消费。
	ActionEnqueue = "enqueue_task"
	// ActionRunCommand 异步执行白名单中的本地命令。
	ActionRunCommand = "run_command"
	// ActionRunGo 执行已注册的进程内 Go 动作。
	ActionRunGo = "run_go_action"
	// ActionRunPlugin 按需执行已加载的 WASI 插件。
	ActionRunPlugin = "run_plugin"
	// ActionNotify 向剪贴板发送端返回系统提示。
	ActionNotify = "notify_sender"
	// ActionBlock 在 enforce 模式下将事件标记为阻断。
	ActionBlock = "block"
)

// ClipboardEvent 是从剪贴板流量归一化得到的自动化事件。
type ClipboardEvent struct {
	EventID   string           `json:"event_id"`
	Timestamp time.Time        `json:"timestamp"`
	Username  string           `json:"username"`
	Source    EventSource      `json:"source"`
	Clipboard ClipboardSummary `json:"clipboard"`
	Transport string           `json:"transport"`
}

// EventSource 记录剪贴板事件可见的网络来源信息。
type EventSource struct {
	IP string `json:"ip"`
}

// ClipboardSummary 是供规则和插件使用的受限剪贴板摘要。
type ClipboardSummary struct {
	Type        string `json:"type"`
	Payload     string `json:"-"`
	PayloadSize int64  `json:"payload_size"`
	FileName    string `json:"filename,omitempty"`
	TextPreview string `json:"text_preview,omitempty"`
	IsEncrypted bool   `json:"is_encrypted"`
}

// RuleSet 是自动化规则 JSONC 配置文件格式。
type RuleSet struct {
	Version int    `json:"version"`
	Mode    string `json:"mode"`
	Rules   []Rule `json:"rules"`
}

// Rule 将匹配条件绑定到一个或多个动作。
type Rule struct {
	ID          string       `json:"id"`
	Enabled     bool         `json:"enabled"`
	Priority    int          `json:"priority"`
	Description string       `json:"description,omitempty"`
	When        Condition    `json:"when"`
	Actions     []RuleAction `json:"actions"`
}

// Condition 是针对 ClipboardEvent 字段的可组合匹配条件。
type Condition struct {
	Field string `json:"field,omitempty"`
	Op    string `json:"op,omitempty"`
	Value any    `json:"value,omitempty"`

	All []Condition `json:"all,omitempty"`
	Any []Condition `json:"any,omitempty"`
	Not *Condition  `json:"not,omitempty"`
}

// RuleAction 描述规则命中后产生的副作用。
type RuleAction struct {
	Type      string         `json:"type"`
	Level     string         `json:"level,omitempty"`
	Message   string         `json:"message,omitempty"`
	TaskType  string         `json:"task_type,omitempty"`
	CommandID string         `json:"command_id,omitempty"`
	GoAction  string         `json:"go_action,omitempty"`
	PluginID  string         `json:"plugin_id,omitempty"`
	Params    map[string]any `json:"params,omitempty"`
}

// Decision 是返回给 relay 链路的自动化决策。
type Decision struct {
	Outcome string
	Notices []SystemNotice
}

// SystemNotice 是自动化返回给用户的受限提示。
type SystemNotice struct {
	Level   string `json:"level"`
	Title   string `json:"title"`
	Message string `json:"message"`
	EventID string `json:"event_id"`
}

// CommandSet 是本地命令白名单的 JSONC 配置文件格式。
type CommandSet struct {
	Commands []CommandSpec `json:"commands"`
}

// CommandSpec 定义一个允许自动化调用的本地命令。
type CommandSpec struct {
	ID             string   `json:"id"`
	Path           string   `json:"path"`
	Args           []string `json:"args"`
	WorkDir        string   `json:"work_dir,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	MaxOutputBytes int64    `json:"max_output_bytes,omitempty"`
}
