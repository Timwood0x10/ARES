package flight

import (
	"context"
	"sync"

	"github.com/Timwood0x10/ares/internal/events"
)

// GenealogyCollector subscribes to EventStore and populates a Genealogy tree.
type GenealogyCollector struct {
	genealogy  *Genealogy
	eventStore events.EventStore
	cancel     context.CancelFunc
	wg         sync.WaitGroup
}

// NewGenealogyCollector creates a new genealogy collector.
func NewGenealogyCollector(eventStore events.EventStore) *GenealogyCollector {
	return &GenealogyCollector{
		genealogy:  NewGenealogy(),
		eventStore: eventStore,
	}
}

// Start begins collecting genealogy data from the event store.
func (c *GenealogyCollector) Start(ctx context.Context) error {
	if c.eventStore == nil {
		return nil
	}

	ctx, c.cancel = context.WithCancel(ctx)

	ch, err := c.eventStore.Subscribe(ctx, events.EventFilter{})
	if err != nil {
		return err
	}

	c.wg.Add(1)
	go c.collectLoop(ctx, ch)

	return nil
}

// Stop stops the collector.
func (c *GenealogyCollector) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	c.wg.Wait()
}

// Genealogy returns the genealogy tree.
func (c *GenealogyCollector) Genealogy() *Genealogy {
	return c.genealogy
}

// collectLoop reads events and updates the genealogy.
func (c *GenealogyCollector) collectLoop(ctx context.Context, ch <-chan *events.Event) {
	defer c.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-ch:
			if !ok {
				return
			}
			c.processEvent(evt)
		}
	}
}

// processEvent routes an event to the appropriate handler.
func (c *GenealogyCollector) processEvent(evt *events.Event) {
	if evt == nil {
		return
	}

	switch evt.Type {
	case events.EventAgentStarted:
		c.handleAgentStarted(evt)
	case events.EventAgentStopped:
		c.handleAgentStopped(evt)
	case events.EventFailoverTriggered:
		c.handleFailoverTriggered(evt)
	case events.EventFailoverCompleted:
		c.handleFailoverCompleted(evt)
	}
}

func (c *GenealogyCollector) handleAgentStarted(evt *events.Event) {
	agentID := evt.StreamID
	agentType := ""
	parentID := ""

	if t, ok := evt.Payload["type"].(string); ok {
		agentType = t
	}
	if p, ok := evt.Payload["parent_id"].(string); ok {
		parentID = p
	}

	if parentID != "" {
		c.genealogy.RecordSpawn(parentID, agentID, agentType, evt.Payload)
	} else {
		// No parent — this is a root agent.
		c.genealogy.RecordRoot(agentID, agentType, evt.Payload)
	}
}

func (c *GenealogyCollector) handleAgentStopped(evt *events.Event) {
	c.genealogy.RecordDeath(evt.StreamID)
}

func (c *GenealogyCollector) handleFailoverTriggered(evt *events.Event) {
	// The failing agent is marked dead.
	if agentID, ok := evt.Payload["agent_id"].(string); ok {
		c.genealogy.RecordDeath(agentID)
	} else {
		c.genealogy.RecordDeath(evt.StreamID)
	}
}

func (c *GenealogyCollector) handleFailoverCompleted(evt *events.Event) {
	// Check if this is a resurrection (old → new) or a promotion.
	oldID, _ := evt.Payload["old_agent_id"].(string)
	newID, _ := evt.Payload["new_agent_id"].(string)

	if oldID != "" && newID != "" {
		c.genealogy.RecordResurrection(oldID, newID)
	} else if newID != "" {
		// Promotion — the new agent takes over.
		c.genealogy.RecordPromotion(newID)
	}
}
