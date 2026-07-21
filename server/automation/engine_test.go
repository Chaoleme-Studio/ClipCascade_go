package automation

import (
	"context"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/clipcascade/pkg/constants"
	"github.com/clipcascade/server/config"
	"github.com/clipcascade/server/model"
)

func TestDefaultShortVideoRuleCreatesTasks(t *testing.T) {
	db := testDB(t)
	engine, err := NewEngine(db, &config.Config{AutomationEnabled: true, AutomationMode: "observe"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	event := ClipboardEvent{
		EventID:   "evt-1",
		Username:  "alice",
		Transport: "websocket",
		Clipboard: ClipboardSummary{
			Type:        constants.TypeText,
			PayloadSize: 48,
			TextPreview: "https://v.douyin.com/example",
		},
	}
	decision := engine.Evaluate(context.Background(), event)
	if decision.Outcome != OutcomeAllow {
		t.Fatalf("unexpected outcome: %s", decision.Outcome)
	}
	var count int64
	if err := db.Model(&model.AutomationTask{}).Where("event_id = ?", "evt-1").Count(&count).Error; err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if count == 0 {
		t.Fatal("expected automation tasks")
	}
}

func TestEncryptedEnvelopeDoesNotMatchTextRule(t *testing.T) {
	db := testDB(t)
	engine, err := NewEngine(db, &config.Config{AutomationEnabled: true, AutomationMode: "observe"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	event := ClipboardEvent{
		EventID:   "evt-2",
		Username:  "alice",
		Transport: "websocket",
		Clipboard: ClipboardSummary{
			Type:        "e2ee_envelope",
			PayloadSize: 256,
			IsEncrypted: true,
		},
	}
	_ = engine.Evaluate(context.Background(), event)
	var count int64
	if err := db.Model(&model.AutomationTask{}).Where("event_id = ?", "evt-2").Count(&count).Error; err != nil {
		t.Fatalf("count tasks: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected no tasks, got %d", count)
	}
}

func TestSecretCandidateTaskInputIsRedacted(t *testing.T) {
	db := testDB(t)
	engine, err := NewEngine(db, &config.Config{AutomationEnabled: true, AutomationMode: "observe"})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"
	event := ClipboardEvent{
		EventID:   "evt-secret",
		Username:  "alice",
		Transport: "websocket",
		Clipboard: ClipboardSummary{
			Type:        constants.TypeText,
			PayloadSize: int64(len(secret)),
			TextPreview: "OPENAI_KEY=" + secret,
		},
	}
	_ = engine.Evaluate(context.Background(), event)

	var tasks []model.AutomationTask
	if err := db.Where("event_id = ?", "evt-secret").Find(&tasks).Error; err != nil {
		t.Fatalf("find tasks: %v", err)
	}
	if len(tasks) == 0 {
		t.Fatal("expected secret candidate tasks")
	}
	for _, task := range tasks {
		if strings.Contains(task.InputJSON, secret) {
			t.Fatalf("task input leaked raw secret: %s", task.InputJSON)
		}
		if !strings.Contains(task.InputJSON, "[REDACTED:openai_key]") {
			t.Fatalf("task input was not redacted: %s", task.InputJSON)
		}
	}
}

func TestMatcherAllRegex(t *testing.T) {
	event := ClipboardEvent{
		Username:  "alice",
		Transport: "websocket",
		Clipboard: ClipboardSummary{
			Type:        constants.TypeText,
			TextPreview: "https://www.kuaishou.com/short-video",
		},
	}
	condition := Condition{All: []Condition{
		{Field: "username", Op: "eq", Value: "alice"},
		{Field: "clipboard.text_preview", Op: "regex", Value: `kuaishou\.com`},
	}}
	if !matchCondition(event, condition) {
		t.Fatal("expected condition to match")
	}
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("db handle: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := model.InitDB(db); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	return db
}
