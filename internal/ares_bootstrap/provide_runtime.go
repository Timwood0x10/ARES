// Package ares_bootstrap — Runtime provider.
package ares_bootstrap

import (
	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

func ProvideRuntime(eventStore ares_events.EventStore) (*ares_runtime.Manager, error) {
	return ares_runtime.New(nil, eventStore, nil), nil
}
