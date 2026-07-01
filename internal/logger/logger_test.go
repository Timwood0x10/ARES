package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestModule(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	log := Module("test-module")
	log.Info("test message", "key", "value")

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	if result["module"] != "test-module" {
		t.Errorf("expected module 'test-module', got %v", result["module"])
	}
	if result["msg"] != "test message" {
		t.Errorf("expected msg 'test message', got %v", result["msg"])
	}
	if result["key"] != "value" {
		t.Errorf("expected key 'value', got %v", result["key"])
	}
}

func TestModuleWith(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	log := ModuleWith("test-module", "tenant_id", "t1")
	log.Info("test message")

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	if result["module"] != "test-module" {
		t.Errorf("expected module 'test-module', got %v", result["module"])
	}
	if result["tenant_id"] != "t1" {
		t.Errorf("expected tenant_id 't1', got %v", result["tenant_id"])
	}
}

func TestModuleWith_ExtraAttrs(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	log := ModuleWith("svc", "region", "us-east", "env", "prod")
	log.Info("deploying")

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	if result["module"] != "svc" {
		t.Errorf("expected module 'svc', got %v", result["module"])
	}
	if result["region"] != "us-east" {
		t.Errorf("expected region 'us-east', got %v", result["region"])
	}
	if result["env"] != "prod" {
		t.Errorf("expected env 'prod', got %v", result["env"])
	}
}

func TestModule_WithContext(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	log := Module("ctx-module")
	log.LogAttrs(context.Background(), slog.LevelInfo, "context msg", slog.String("trace_id", "abc123"))

	var result map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse log output: %v", err)
	}

	if result["module"] != "ctx-module" {
		t.Errorf("expected module 'ctx-module', got %v", result["module"])
	}
	if result["trace_id"] != "abc123" {
		t.Errorf("expected trace_id 'abc123', got %v", result["trace_id"])
	}
}
