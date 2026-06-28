package monitoring

import (
	"context"
	"log/slog"

	"github.com/Timwood0x10/ares/internal/ares_events"
	"github.com/Timwood0x10/ares/internal/ares_runtime"
)

// Collector subscribes to the event bus and dispatches events to all
// monitoring sub-components. It manages the event subscription lifecycle.
type Collector struct {
	bus      ares_runtime.EventBus
	mainPage *MainPage
	logger   *slog.Logger
	cancel   context.CancelFunc
}

// CollectorOption configures the Collector.
type CollectorOption func(*Collector)

// WithCollectorLogger sets a custom logger for the collector.
func WithCollectorLogger(logger *slog.Logger) CollectorOption {
	return func(c *Collector) {
		c.logger = logger
	}
}

// NewCollector creates a Collector that dispatches events to the given MainPage.
func NewCollector(bus ares_runtime.EventBus, mainPage *MainPage, opts ...CollectorOption) *Collector {
	if bus == nil || mainPage == nil {
		return nil
	}
	c := &Collector{
		bus:      bus,
		mainPage: mainPage,
		logger:   slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Start subscribes to all events on the bus and dispatches them to the
// MainPage. The subscription is cancelled when ctx is done or Stop is called.
func (c *Collector) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel

	ch, err := c.bus.Subscribe(ctx, ares_events.EventFilter{})
	if err != nil {
		cancel()
		return err
	}

	go c.receiveLoop(ctx, ch)
	return nil
}

// Stop cancels the event subscription.
func (c *Collector) Stop() {
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

// receiveLoop reads events from the channel and dispatches them.
func (c *Collector) receiveLoop(ctx context.Context, ch <-chan *ares_events.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			c.mainPage.HandleEvent(evt)
		}
	}
}
