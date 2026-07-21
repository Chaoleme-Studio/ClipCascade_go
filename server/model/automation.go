package model

import "time"

const (
	// AutomationTaskPending 表示任务已入队等待处理。
	AutomationTaskPending = "pending"
	// AutomationTaskRunning 表示任务已经开始且尚未结束。
	AutomationTaskRunning = "running"
	// AutomationTaskSucceeded 表示任务已成功完成。
	AutomationTaskSucceeded = "succeeded"
	// AutomationTaskFailed 表示任务因自动化错误结束。
	AutomationTaskFailed = "failed"
)

// AutomationTask 记录异步自动化任务及序列化结果。
type AutomationTask struct {
	ID         uint      `gorm:"primarykey" json:"id"`
	TaskID     string    `gorm:"uniqueIndex;size:64;not null" json:"task_id"`
	EventID    string    `gorm:"index;size:64;not null" json:"event_id"`
	Username   string    `gorm:"index;size:50;not null" json:"username"`
	RuleID     string    `gorm:"index;size:120" json:"rule_id"`
	ActionType string    `gorm:"size:40;not null" json:"action_type"`
	TaskType   string    `gorm:"index;size:80;not null" json:"task_type"`
	Status     string    `gorm:"index;size:20;not null" json:"status"`
	InputJSON  string    `gorm:"type:text" json:"input_json"`
	ResultJSON string    `gorm:"type:text" json:"result_json"`
	Error      string    `gorm:"type:text" json:"error"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// AutomationAudit 记录规则命中和策略决策，供诊断使用。
type AutomationAudit struct {
	ID            uint      `gorm:"primarykey" json:"id"`
	EventID       string    `gorm:"index;size:64;not null" json:"event_id"`
	Username      string    `gorm:"index;size:50;not null" json:"username"`
	RuleID        string    `gorm:"index;size:120" json:"rule_id"`
	ActionType    string    `gorm:"size:40;not null" json:"action_type"`
	Decision      string    `gorm:"index;size:30;not null" json:"decision"`
	ClipboardType string    `gorm:"size:40" json:"clipboard_type"`
	PayloadSize   int64     `json:"payload_size"`
	IsEncrypted   bool      `json:"is_encrypted"`
	Level         string    `gorm:"size:20" json:"level"`
	Error         string    `gorm:"type:text" json:"error"`
	CreatedAt     time.Time `json:"created_at"`
}

// AutomationPluginSetting 存储插件启停状态的管理员覆盖值。
type AutomationPluginSetting struct {
	ID              uint      `gorm:"primarykey" json:"id"`
	PluginID        string    `gorm:"uniqueIndex;size:120;not null" json:"plugin_id"`
	EnabledOverride *bool     `json:"enabled_override"`
	UpdatedAt       time.Time `json:"updated_at"`
	CreatedAt       time.Time `json:"created_at"`
}
