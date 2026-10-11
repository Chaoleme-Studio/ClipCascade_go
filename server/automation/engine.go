package automation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/server/config"
	"github.com/clipcascade/server/model"
)

// GoAction 是服务端注册的进程内自动化动作。
type GoAction func(context.Context, ClipboardEvent, RuleAction) (map[string]any, error)

// Engine 负责匹配剪贴板事件并派发对应动作。
type Engine struct {
	db         *gorm.DB
	cfg        *config.Config
	rules      []Rule
	commands   map[string]CommandSpec
	actions    map[string]GoAction
	plugins    PluginCatalog
	pluginByID map[string]PluginInfo
	mode       string
}

// NewEngine 加载自动化规则、命令白名单和插件清单。
func NewEngine(db *gorm.DB, cfg *config.Config) (*Engine, error) {
	e := &Engine{
		db:         db,
		cfg:        cfg,
		commands:   make(map[string]CommandSpec),
		actions:    make(map[string]GoAction),
		pluginByID: make(map[string]PluginInfo),
		mode:       strings.TrimSpace(cfg.AutomationMode),
	}
	if e.mode == "" {
		e.mode = "observe"
	}
	e.RegisterGoAction("url_platform_candidate", urlPlatformCandidateAction)

	rules, mode, err := loadRules(cfg.AutomationRulesPath)
	if err != nil {
		return nil, err
	}
	if mode != "" {
		e.mode = mode
	}
	if len(rules) == 0 {
		rules = defaultRules()
	}
	sort.SliceStable(rules, func(i, j int) bool {
		return rules[i].Priority < rules[j].Priority
	})
	e.rules = rules

	commands, err := loadCommands(cfg.AutomationCommandsPath)
	if err != nil {
		return nil, err
	}
	for _, cmd := range commands {
		if strings.TrimSpace(cmd.ID) != "" {
			e.commands[cmd.ID] = cmd
		}
	}
	plugins, err := loadPlugins(cfg.AutomationPluginsDir)
	if err != nil {
		return nil, err
	}
	e.plugins = plugins
	for _, plugin := range plugins.Plugins {
		if plugin.Manifest.ID != "" {
			e.pluginByID[plugin.Manifest.ID] = plugin
		}
	}
	return e, nil
}

// RegisterGoAction 按名称注册进程内动作。
func (e *Engine) RegisterGoAction(name string, fn GoAction) {
	if e == nil || fn == nil || strings.TrimSpace(name) == "" {
		return
	}
	e.actions[strings.TrimSpace(name)] = fn
}

// Evaluate 对事件执行自动化规则并返回 relay 决策。
func (e *Engine) Evaluate(ctx context.Context, event ClipboardEvent) (decision Decision) {
	decision = Decision{Outcome: OutcomeAllow}
	if e == nil || !e.cfg.AutomationEnabled {
		return decision
	}
	if event.EventID == "" {
		event.EventID = uuid.NewString()
	}
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	defer func() {
		if r := recover(); r != nil {
			slog.Warn("automation: recovered panic", "event_id", event.EventID, "error", r)
			decision = Decision{Outcome: OutcomeAllow}
		}
	}()

	for _, rule := range e.rules {
		if !rule.Enabled || !matchCondition(event, rule.When) {
			continue
		}
		for _, action := range rule.Actions {
			if notice, block := e.executeAction(ctx, event, rule, action); notice != nil {
				decision.Notices = append(decision.Notices, *notice)
			} else if block {
				decision.Outcome = OutcomeBlock
			}
		}
	}
	if e.mode != "enforce" && decision.Outcome == OutcomeBlock {
		_ = e.writeAudit(event, "observe", ActionBlock, "allow", "block ignored because automation mode is observe")
		decision.Outcome = OutcomeAllow
	}
	return decision
}

func (e *Engine) executeAction(ctx context.Context, event ClipboardEvent, rule Rule, action RuleAction) (*SystemNotice, bool) {
	actionType := strings.TrimSpace(action.Type)
	switch actionType {
	case ActionAudit:
		level := action.Level
		if level == "" {
			level = "info"
		}
		_ = e.writeAudit(event, rule.ID, ActionAudit, level, "")
	case ActionEnqueue:
		taskType := action.TaskType
		if taskType == "" {
			taskType = rule.ID
		}
		if _, err := e.createTask(event, rule.ID, ActionEnqueue, taskType, model.AutomationTaskPending, nil, ""); err != nil {
			_ = e.writeAudit(event, rule.ID, ActionEnqueue, "error", err.Error())
		}
	case ActionRunCommand:
		task, err := e.createTask(event, rule.ID, ActionRunCommand, action.CommandID, model.AutomationTaskRunning, nil, "")
		if err != nil {
			_ = e.writeAudit(event, rule.ID, ActionRunCommand, "error", err.Error())
			return nil, false
		}
		go e.runCommandTask(event, action, task)
	case ActionRunGo:
		task, err := e.createTask(event, rule.ID, ActionRunGo, action.GoAction, model.AutomationTaskRunning, nil, "")
		if err != nil {
			_ = e.writeAudit(event, rule.ID, ActionRunGo, "error", err.Error())
			return nil, false
		}
		e.runGoAction(ctx, event, action, task)
	case ActionRunPlugin:
		taskType := action.PluginID
		if taskType == "" {
			taskType = rule.ID
		}
		task, err := e.createTask(event, rule.ID, ActionRunPlugin, taskType, model.AutomationTaskRunning, nil, "")
		if err != nil {
			_ = e.writeAudit(event, rule.ID, ActionRunPlugin, "error", err.Error())
			return nil, false
		}
		go e.runPluginAction(event, action, task)
	case ActionNotify:
		msg := action.Message
		if msg == "" {
			msg = "Automation rule matched: " + rule.ID
		}
		return &SystemNotice{Level: "info", Title: "ClipCascade", Message: msg, EventID: event.EventID}, false
	case ActionBlock:
		_ = e.writeAudit(event, rule.ID, ActionBlock, "warn", "")
		return nil, true
	default:
		_ = e.writeAudit(event, rule.ID, actionType, "warn", "unknown automation action")
	}
	return nil, false
}

func (e *Engine) createTask(event ClipboardEvent, ruleID, actionType, taskType, status string, result map[string]any, errText string) (*model.AutomationTask, error) {
	storedEvent := sanitizeEventForStorage(event)
	input, _ := json.Marshal(map[string]any{
		"event": storedEvent,
		"rule":  ruleID,
	})
	resultJSON := ""
	if result != nil {
		data, _ := json.Marshal(result)
		resultJSON = string(data)
	}
	task := &model.AutomationTask{
		TaskID:     uuid.NewString(),
		EventID:    event.EventID,
		Username:   event.Username,
		RuleID:     ruleID,
		ActionType: actionType,
		TaskType:   taskType,
		Status:     status,
		InputJSON:  string(input),
		ResultJSON: resultJSON,
		Error:      errText,
	}
	if err := e.db.Create(task).Error; err != nil {
		return nil, err
	}
	return task, nil
}

func (e *Engine) writeAudit(event ClipboardEvent, ruleID, actionType, level, errText string) error {
	if level == "" {
		level = "info"
	}
	return e.db.Create(&model.AutomationAudit{
		EventID:       event.EventID,
		Username:      event.Username,
		RuleID:        ruleID,
		ActionType:    actionType,
		Decision:      e.mode,
		ClipboardType: event.Clipboard.Type,
		PayloadSize:   event.Clipboard.PayloadSize,
		IsEncrypted:   event.Clipboard.IsEncrypted,
		Level:         level,
		Error:         errText,
	}).Error
}

func (e *Engine) runGoAction(ctx context.Context, event ClipboardEvent, action RuleAction, task *model.AutomationTask) {
	name := strings.TrimSpace(action.GoAction)
	fn := e.actions[name]
	if fn == nil {
		e.finishTask(task, model.AutomationTaskFailed, nil, "unknown Go action: "+name)
		return
	}
	result, err := fn(ctx, event, action)
	if err != nil {
		e.finishTask(task, model.AutomationTaskFailed, result, err.Error())
		return
	}
	e.finishTask(task, model.AutomationTaskSucceeded, result, "")
}

func (e *Engine) runPluginAction(event ClipboardEvent, action RuleAction, task *model.AutomationTask) {
	taskID := ""
	if task != nil {
		taskID = task.TaskID
	}
	result, err := e.runPluginTask(event, action, taskID)
	if err != nil {
		e.finishTask(task, model.AutomationTaskFailed, result, err.Error())
		return
	}
	e.finishTask(task, model.AutomationTaskSucceeded, result, "")
}

func (e *Engine) runCommandTask(event ClipboardEvent, action RuleAction, task *model.AutomationTask) {
	spec, ok := e.commands[action.CommandID]
	if !ok {
		e.finishTask(task, model.AutomationTaskFailed, nil, "command is not in allowlist: "+action.CommandID)
		return
	}
	if !filepath.IsAbs(spec.Path) {
		e.finishTask(task, model.AutomationTaskFailed, nil, "allowlist command path must be absolute")
		return
	}
	timeout := time.Duration(e.cfg.AutomationCommandTimeoutSeconds) * time.Second
	if spec.TimeoutSeconds > 0 {
		timeout = time.Duration(spec.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	args := make([]string, 0, len(spec.Args))
	for _, arg := range spec.Args {
		args = append(args, renderCommandTemplate(arg, event))
	}
	if spec.WorkDir != "" && !filepath.IsAbs(spec.WorkDir) {
		e.finishTask(task, model.AutomationTaskFailed, nil, "command work_dir must be absolute when set")
		return
	}
	cmd := exec.CommandContext(ctx, spec.Path, args...)
	if spec.WorkDir != "" {
		cmd.Dir = spec.WorkDir
	}
	outputLimit := commandOutputLimit(e.cfg, spec)
	var out limitedBuffer
	out.max = outputLimit
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	output := out.String()
	if out.truncated {
		output += "\n[truncated]"
	}
	result := map[string]any{"output": output}
	if ctx.Err() == context.DeadlineExceeded {
		e.finishTask(task, model.AutomationTaskFailed, result, "command timed out")
		return
	}
	if err != nil {
		e.finishTask(task, model.AutomationTaskFailed, result, err.Error())
		return
	}
	e.finishTask(task, model.AutomationTaskSucceeded, result, "")
}

func (e *Engine) finishTask(task *model.AutomationTask, status string, result map[string]any, errText string) {
	if task == nil {
		return
	}
	updates := map[string]any{"status": status, "error": errText}
	if result != nil {
		data, _ := json.Marshal(result)
		updates["result_json"] = string(data)
	}
	if err := e.db.Model(task).Updates(updates).Error; err != nil {
		slog.Warn("automation: failed to update task", "task_id", task.TaskID, "error", err)
	}
}

func loadRules(path string) ([]Rule, string, error) {
	if strings.TrimSpace(path) == "" {
		return nil, "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", err
	}
	var rules RuleSet
	if err := unmarshalJSONC(data, &rules); err != nil {
		return nil, "", err
	}
	return rules.Rules, rules.Mode, nil
}

func loadCommands(path string) ([]CommandSpec, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var set CommandSet
	if err := unmarshalJSONC(data, &set); err != nil {
		return nil, err
	}
	return set.Commands, nil
}

func defaultRules() []Rule {
	return []Rule{
		{
			ID:       "secret-candidate",
			Enabled:  true,
			Priority: 90,
			When: Condition{All: []Condition{
				{Field: "clipboard.type", Op: "eq", Value: "text"},
				{Field: "clipboard.text_preview", Op: "regex", Value: `(?i)(github_pat_|\bgh[pousr]_|\bsk-(proj-)?|\b(AKIA|ASIA)[A-Z0-9]{12,}|-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----|\bAIza[0-9A-Za-z_-]{20,}|\b(sk_live|rk_live)_|\bxox[baprs]-|\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.|\b(postgres|postgresql|mysql|mongodb|redis)://[^\s:/?#]+:[^\s@]+@|\bbearer\s+[A-Za-z0-9._~+/=-]{24,})`},
			}},
			Actions: []RuleAction{
				{Type: ActionAudit, Level: "warn"},
				{Type: ActionEnqueue, TaskType: "secret_candidate"},
				{Type: ActionRunPlugin, PluginID: "clipcascade.secret-detector"},
			},
		},
		{
			ID:       "url-platform-candidate",
			Enabled:  true,
			Priority: 100,
			When: Condition{All: []Condition{
				{Field: "clipboard.type", Op: "eq", Value: "text"},
				{Field: "clipboard.text_preview", Op: "regex", Value: `(?i)(douyin\.com|v\.douyin\.com|iesdouyin\.com|kuaishou\.com|v\.kuaishou\.com|gifshow\.com)`},
			}},
			Actions: []RuleAction{
				{Type: ActionAudit, Level: "info"},
				{Type: ActionEnqueue, TaskType: "url_platform_candidate"},
				{Type: ActionRunPlugin, PluginID: "clipcascade.url-platform-candidate"},
			},
		},
	}
}

var urlRe = regexp.MustCompile(`https?://[^\s]+`)

func urlPlatformCandidateAction(_ context.Context, event ClipboardEvent, _ RuleAction) (map[string]any, error) {
	text := event.Clipboard.TextPreview
	url := urlRe.FindString(text)
	platform := "unknown"
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "douyin.com") || strings.Contains(lower, "iesdouyin.com"):
		platform = "douyin"
	case strings.Contains(lower, "kuaishou.com") || strings.Contains(lower, "gifshow.com"):
		platform = "kuaishou"
	}
	return map[string]any{
		"platform": platform,
		"url":      url,
		"reserved": "download action is intentionally not implemented in this phase",
	}, nil
}

func renderCommandTemplate(tpl string, event ClipboardEvent) string {
	// payload 渲染进 argv 前截断：防止巨量剪贴板内容直接进入命令行参数
	// （argv 上限受 ARG_MAX 约束，且白名单二进制若自行解释参数存在注入升级面）。
	payload := event.Clipboard.Payload
	if len(payload) > constants.MaxCommandArgPayloadBytes {
		payload = payload[:constants.MaxCommandArgPayloadBytes] + "…[truncated]"
	}
	replacements := map[string]string{
		"{{.event_id}}":               event.EventID,
		"{{.username}}":               event.Username,
		"{{.clipboard.type}}":         event.Clipboard.Type,
		"{{.clipboard.text_preview}}": event.Clipboard.TextPreview,
		"{{.clipboard.payload}}":      payload,
		"{{.clipboard.filename}}":     event.Clipboard.FileName,
		"{{.clipboard.payload_size}}": fmt.Sprintf("%d", event.Clipboard.PayloadSize),
		"{{.clipboard.is_encrypted}}": fmt.Sprintf("%t", event.Clipboard.IsEncrypted),
	}
	out := tpl
	for key, value := range replacements {
		out = strings.ReplaceAll(out, key, value)
	}
	return out
}

func commandOutputLimit(cfg *config.Config, spec CommandSpec) int64 {
	limit := cfg.AutomationCommandMaxOutputBytes
	if spec.MaxOutputBytes > 0 {
		limit = spec.MaxOutputBytes
	}
	if limit <= 0 {
		limit = 64 * 1024
	}
	return limit
}

type limitedBuffer struct {
	bytes.Buffer
	max       int64
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.max <= 0 {
		return len(p), nil
	}
	remaining := b.max - int64(b.Buffer.Len())
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if int64(len(p)) > remaining {
		b.truncated = true
		_, _ = b.Buffer.Write(p[:int(remaining)])
		return len(p), nil
	}
	_, _ = b.Buffer.Write(p)
	return len(p), nil
}
