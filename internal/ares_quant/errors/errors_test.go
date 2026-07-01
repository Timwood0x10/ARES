package errors

import (
	"errors"
	"testing"
)

func TestErrNoMarketData_IsNotNil(t *testing.T) {
	if ErrNoMarketData == nil {
		t.Fatal("ErrNoMarketData must not be nil")
	}
}

func TestErrNoMarketData_ErrorMessage(t *testing.T) {
	want := "no market data available"
	if got := ErrNoMarketData.Error(); got != want {
		t.Errorf("ErrNoMarketData.Error() = %q, want %q", got, want)
	}
}

func TestErrNoMarketData_ErrorsIs(t *testing.T) {
	err := ErrNoMarketData
	if !errors.Is(err, ErrNoMarketData) {
		t.Error("errors.Is(ErrNoMarketData, ErrNoMarketData) should be true")
	}
}

func TestErrNoMarketData_ErrorsIsOther(t *testing.T) {
	other := errors.New("no market data available")
	if errors.Is(ErrNoMarketData, other) {
		t.Error("errors.Is should not match a different error instance even with same message")
	}
}
