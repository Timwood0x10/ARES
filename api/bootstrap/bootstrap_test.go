package bootstrap

import (
	"context"
	"testing"

	arenasvc "github.com/Timwood0x10/ares/api/service/arena"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Runtime == nil {
		t.Fatal("expected non-nil Runtime config")
	}
	if cfg.Evolution == nil {
		t.Fatal("expected non-nil Evolution config")
	}
	if cfg.Dashboard == nil {
		t.Fatal("expected non-nil Dashboard config")
	}
}

func TestNewNilConfig(t *testing.T) {
	ares, err := New(context.Background(), nil)
	if err != nil {
		t.Fatalf("New(nil): %v", err)
	}
	if ares == nil {
		t.Fatal("expected non-nil ARES")
	}
	if ares.Runtime != nil {
		t.Log("expected nil Runtime (no AresConfig)")
	}
}

func TestNewCustomConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Dashboard.Enabled = true
	ares, err := New(context.Background(), cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if ares.Runtime != nil {
		t.Log("expected nil Runtime (no AresConfig)")
	}
}

func TestStartStopNoRuntime(t *testing.T) {
	ares, err := New(context.Background(), DefaultConfig())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := ares.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := ares.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func assertArenaNotInitialized(t *testing.T) *ARES {
	t.Helper()
	ares := &ARES{}
	return ares
}

func TestRunEvolutionNotInitialized(t *testing.T) {
	ares := assertArenaNotInitialized(t)
	_, err := ares.RunEvolution(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when evolution not initialized")
	}
}

func TestRunIdleEvolutionNotInitialized(t *testing.T) {
	ares := assertArenaNotInitialized(t)
	err := ares.RunIdleEvolution(context.Background(), 1)
	if err == nil {
		t.Fatal("expected error when evolution not initialized")
	}
}

func TestLatestReportNotInitialized(t *testing.T) {
	ares := assertArenaNotInitialized(t)
	_, err := ares.LatestReport()
	if err == nil {
		t.Fatal("expected error when evolution not initialized")
	}
}

func TestArenaActionNotInitialized(t *testing.T) {
	ares := assertArenaNotInitialized(t)
	result := ares.ExecuteArenaAction(context.Background(), arenasvc.Action{})
	if result.Success {
		t.Fatal("expected failure when arena not initialized")
	}
}

func TestStopWithNilFields(t *testing.T) {
	ares := &ARES{}
	if err := ares.Stop(); err != nil {
		t.Fatalf("Stop on zero ARES: %v", err)
	}
}

func TestDashboardConfigDefault(t *testing.T) {
	dc := &DashboardConfig{}
	if dc.Enabled {
		t.Fatal("expected DashboardConfig.Enabled to be false by default")
	}
}
