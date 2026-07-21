package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDetectSecretsDoesNotReturnRawSecret(t *testing.T) {
	secret := "sk-proj-abcdefghijklmnopqrstuvwxyz1234567890"
	result := detectSecrets("token="+secret+" github_pat_abcdefghijklmnopqrstuvwxyz1234567890", false)
	if !result.Matched {
		t.Fatal("expected secret candidate")
	}
	if result.Total < 2 {
		t.Fatalf("expected multiple findings, got %d", result.Total)
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result: %v", err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("result leaked raw secret: %s", string(data))
	}
}

func TestDetectSecretsSkipsEncryptedEvent(t *testing.T) {
	result := detectSecrets("sk-proj-abcdefghijklmnopqrstuvwxyz1234567890", true)
	if result.Matched {
		t.Fatalf("encrypted event should not be inspected: %+v", result)
	}
}
