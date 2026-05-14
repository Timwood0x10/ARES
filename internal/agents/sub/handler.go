package sub

import (
	"context"

	"github.com/Timwood0x10/goagent/internal/core/errors"
	"github.com/Timwood0x10/goagent/internal/protocol/ahp"
)

// messageHandler handles incoming AHP messages.
type messageHandler struct {
	agentID string
}

// NewMessageHandler creates a new MessageHandler.
func NewMessageHandler(agentID string) MessageHandler {
	return &messageHandler{
		agentID: agentID,
	}
}

// Handle processes an incoming message.
func (h *messageHandler) Handle(ctx context.Context, msg *ahp.AHPMessage) error {
	if msg == nil {
		return errors.ErrNilPointer
	}

	switch msg.Method {
	case ahp.AHPMethodTask:
		return h.handleTaskMessage(ctx, msg)
	case ahp.AHPMethodACK:
		return h.handleAckMessage(ctx, msg)
	case ahp.AHPMethodHeartbeat:
		return nil // Heartbeat acknowledged
	default:
		return errors.ErrInvalidMessage
	}
}

func (h *messageHandler) handleTaskMessage(ctx context.Context, msg *ahp.AHPMessage) error {
	// Task handling is done by executor
	// This is for protocol-level message acknowledgment
	return nil
}

func (h *messageHandler) handleAckMessage(ctx context.Context, msg *ahp.AHPMessage) error {
	// Handle acknowledgment
	return nil
}
