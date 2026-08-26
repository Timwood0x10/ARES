package main

import (
	"context"
	"testing"
	"time"

	"github.com/Timwood0x10/ares/internal/ares_config"
)

func TestParseChaosInterval(t *testing.T) {
	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"", 5 * time.Minute},
		{"invalid", 5 * time.Minute},
		{"0s", 5 * time.Minute},
		{"10m", 10 * time.Minute},
		{"30s", 30 * time.Second},
		{"1h", time.Hour},
	}
	for _, tt := range tests {
		got := parseChaosInterval(tt.input, 5*time.Minute)
		if got != tt.expected {
			t.Errorf("parseChaosInterval(%q) = %v, want %v", tt.input, got, tt.expected)
		}
	}
}

func TestWireChaos_Disabled(t *testing.T) {
	cfg := &ares_config.Config{
		Kernel: ares_config.KernelConfig{
			Chaos: ares_config.ChaosConfig{
				Enabled: false,
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic or start any goroutines
	wireChaos(ctx, cfg, nil, nil)
}

func TestWireChaos_ShadowDefault(t *testing.T) {
	cfg := &ares_config.Config{
		Kernel: ares_config.KernelConfig{
			Chaos: ares_config.ChaosConfig{
				Enabled:  true,
				Mode:     "", // empty = shadow
				Interval: "100ms",
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wireChaos(ctx, cfg, nil, nil)

	// Let it tick once
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)
}

func TestWireChaos_LiveWithoutAllowLive_FallsBackToShadow(t *testing.T) {
	cfg := &ares_config.Config{
		Kernel: ares_config.KernelConfig{
			Chaos: ares_config.ChaosConfig{
				Enabled:   true,
				Mode:      "live",
				AllowLive: false, // should fall back to shadow
				Interval:  "100ms",
			},
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wireChaos(ctx, cfg, nil, nil)

	// Let it tick once — should be shadow, not live
	time.Sleep(200 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)
}
