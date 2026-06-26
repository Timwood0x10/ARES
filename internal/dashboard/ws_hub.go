package dashboard

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Lock ordering: always acquire h.mu (WSHub) before c.mu (WSClient).
// This invariant is upheld by Subscribe/Unsubscribe and removeClient.

// WSHub manages WebSocket client connections and channel-based message routing.
type WSHub struct {
	clients    map[*WSClient]struct{}
	channels   map[string]map[*WSClient]struct{}
	register   chan *WSClient
	unregister chan *WSClient
	broadcast  chan *WSMessage
	mu         sync.RWMutex
	done       chan struct{}
	stopOnce   sync.Once
}

// WSClient represents a single WebSocket connection.
type WSClient struct {
	hub      *WSHub
	conn     *websocket.Conn
	send     chan []byte
	channels map[string]struct{}
	mu       sync.Mutex
	wg       sync.WaitGroup
}

// NewWSHub creates a new WSHub.
func NewWSHub() *WSHub {
	return &WSHub{
		clients:    make(map[*WSClient]struct{}),
		channels:   make(map[string]map[*WSClient]struct{}),
		register:   make(chan *WSClient),
		unregister: make(chan *WSClient),
		broadcast:  make(chan *WSMessage, 256),
		done:       make(chan struct{}),
	}
}

// Run starts the hub's main event loop. Call this in a goroutine.
func (h *WSHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = struct{}{}
			h.mu.Unlock()

		case client := <-h.unregister:
			h.mu.Lock()
			h.removeClient(client)
			h.mu.Unlock()

		case msg := <-h.broadcast:
			data, err := json.Marshal(msg)
			if err != nil {
				continue
			}
			h.mu.RLock()
			if msg.Channel != "" {
				// Send to channel subscribers.
				if subs, ok := h.channels[msg.Channel]; ok {
					for client := range subs {
						h.sendRawToClient(client, data)
					}
				}
			} else {
				// Broadcast to all clients.
				for client := range h.clients {
					h.sendRawToClient(client, data)
				}
			}
			h.mu.RUnlock()

		case <-h.done:
			return
		}
	}
}

// Stop shuts down the hub. Safe to call multiple times.
func (h *WSHub) Stop() {
	h.stopOnce.Do(func() {
		close(h.done)
	})
}

// BroadcastToChannel sends a message to all subscribers of a channel.
func (h *WSHub) BroadcastToChannel(channel string, msg *WSMessage) {
	msg.Channel = channel
	msg.TS = time.Now()
	select {
	case h.broadcast <- msg:
	default:
		// Drop message if buffer is full.
	}
}

// BroadcastAll sends a message to all connected clients.
func (h *WSHub) BroadcastAll(msg *WSMessage) {
	msg.TS = time.Now()
	select {
	case h.broadcast <- msg:
	default:
	}
}

// Register adds a client to the hub.
func (h *WSHub) Register(client *WSClient) {
	h.register <- client
}

// Unregister removes a client from the hub.
func (h *WSHub) Unregister(client *WSClient) {
	h.unregister <- client
}

// ClientCount returns the number of connected clients.
func (h *WSHub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ChannelCount returns the number of active channels.
func (h *WSHub) ChannelCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.channels)
}

// removeClient removes a client from all structures. Caller must hold h.mu.
func (h *WSHub) removeClient(client *WSClient) {
	if _, ok := h.clients[client]; !ok {
		return
	}

	delete(h.clients, client)

	// Acquire client.mu before reading client.channels to prevent data race.
	// Lock ordering: client.mu before h.mu is maintained since h.mu is already held.
	client.mu.Lock()
	channelsCopy := make(map[string]struct{})
	for ch := range client.channels {
		channelsCopy[ch] = struct{}{}
	}
	client.mu.Unlock()

	// Remove from all channels.
	for ch := range channelsCopy {
		if subs, ok := h.channels[ch]; ok {
			delete(subs, client)
			if len(subs) == 0 {
				delete(h.channels, ch)
			}
		}
	}

	close(client.send)
}

// sendRawToClient queues pre-marshaled data for a client. Caller must hold h.mu.
// No need to lock client.mu: client.send is a buffered channel and is only closed
// under h.mu.Lock() (in removeClient). Holding h.mu (even RLock) prevents removeClient
// from acquiring the write lock, so client.send cannot be closed concurrently.
func (h *WSHub) sendRawToClient(client *WSClient, data []byte) {
	select {
	case client.send <- data:
	default:
		// Client buffer full, drop message.
	}
}

// NewWSClient creates a new WebSocket client.
func NewWSClient(hub *WSHub, conn *websocket.Conn) *WSClient {
	return &WSClient{
		hub:      hub,
		conn:     conn,
		send:     make(chan []byte, 256),
		channels: make(map[string]struct{}),
	}
}

// Subscribe adds the client to a channel.
// Lock ordering: c.hub.mu before c.mu (matching removeClient).
func (c *WSClient) Subscribe(channel string) {
	c.hub.mu.Lock()
	c.mu.Lock()
	c.channels[channel] = struct{}{}
	c.mu.Unlock()

	if _, ok := c.hub.channels[channel]; !ok {
		c.hub.channels[channel] = make(map[*WSClient]struct{})
	}
	c.hub.channels[channel][c] = struct{}{}
	c.hub.mu.Unlock()
}

// Unsubscribe removes the client from a channel.
func (c *WSClient) Unsubscribe(channel string) {
	c.hub.mu.Lock()
	c.mu.Lock()
	delete(c.channels, channel)
	c.mu.Unlock()

	if subs, ok := c.hub.channels[channel]; ok {
		delete(subs, c)
		if len(subs) == 0 {
			delete(c.hub.channels, channel)
		}
	}
	c.hub.mu.Unlock()
}

// Start launches the ReadPump and WritePump goroutines for this client.
// The caller must provide the ping interval for the WritePump.
func (c *WSClient) Start(pingInterval time.Duration) {
	c.wg.Add(2)
	go func() {
		defer c.wg.Done()
		c.ReadPump()
	}()
	go func() {
		defer c.wg.Done()
		c.WritePump(pingInterval)
	}()
}

// Wait blocks until both ReadPump and WritePump have exited.
func (c *WSClient) Wait() {
	c.wg.Wait()
}

// ReadPump reads messages from the WebSocket connection.
func (c *WSClient) ReadPump() {
	defer func() {
		c.hub.Unregister(c)
		if err := c.conn.Close(); err != nil {
			slog.Warn("ws: close connection failed", "error", err)
		}
	}()

	c.conn.SetReadLimit(4096)
	if err := c.conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
		slog.Warn("ws: set read deadline", "error", err)
	}
	c.conn.SetPongHandler(func(string) error {
		if err := c.conn.SetReadDeadline(time.Now().Add(60 * time.Second)); err != nil {
			slog.Warn("ws: set read deadline", "error", err)
		}
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			return
		}

		var clientMsg WSClientMessage
		if err := json.Unmarshal(message, &clientMsg); err != nil {
			continue
		}

		switch clientMsg.Type {
		case WSTypeSubscribe:
			if clientMsg.Channel != "" {
				c.Subscribe(clientMsg.Channel)
			}
		case WSTypeUnsubscribe:
			if clientMsg.Channel != "" {
				c.Unsubscribe(clientMsg.Channel)
			}
		case WSTypePing:
			pong := WSMessage{
				Type: WSTypePong,
				TS:   time.Now(),
			}
			data, _ := json.Marshal(pong)
			c.mu.Lock()
			select {
			case c.send <- data:
			default:
			}
			c.mu.Unlock()
		}
	}
}

// WritePump writes messages from the send channel to the WebSocket connection.
func (c *WSClient) WritePump(pingInterval time.Duration) {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		if err := c.conn.Close(); err != nil {
			slog.Warn("ws: close connection failed", "error", err)
		}
	}()

	for {
		select {
		case message, ok := <-c.send:
			if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				slog.Warn("ws: set write deadline", "error", err)
			}
			if !ok {
				if err := c.conn.WriteMessage(websocket.CloseMessage, []byte{}); err != nil {
					slog.Warn("ws: write close message", "error", err)
				}
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				return
			}

		case <-ticker.C:
			if err := c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
				slog.Warn("ws: set write deadline", "error", err)
			}
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
