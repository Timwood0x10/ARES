package services

import (
	"testing"
)

func TestCoreDefaultRetrievalPlan(t *testing.T) {
	plan := DefaultRetrievalPlan()
	if plan == nil {
		t.Fatal("expected non-nil plan")
	}
}

func TestCoreDefaultQueryPriorityConfig(t *testing.T) {
	cfg := DefaultQueryPriorityConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}
