package realtime

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 50 * time.Second
	maxMessageSize = 4096
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	// 生產環境需校驗 Origin
	CheckOrigin: func(r *http.Request) bool { return true },
}

// Message is the envelope for all WebSocket messages.
type Message struct {
	Type    string `json:"type"`
	Payload any    `json:"payload,omitempty"`
}

// ClientConfig stores the user's real-time preferences.
type ClientConfig struct {
	Enabled             bool  `json:"enabled"`
	RankingIntervalSec  int   `json:"ranking_interval_sec"`
	FactionAlert        bool  `json:"faction_alert"`
}

// Client represents a single WebSocket connection.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	userID string
	raceID string
	config ClientConfig
}

// Hub manages all WebSocket clients for a single race.
// Multiple Hub instances (one per race) run concurrently.
// Cross-instance broadcasting is handled via Redis Pub/Sub.
type Hub struct {
	raceID    string
	clients   map[*Client]bool
	mu        sync.RWMutex
	broadcast chan []byte
	join      chan *Client
	leave     chan *Client
	rdb       *redis.Client
	cancel    context.CancelFunc
}

// Manager manages all Hubs (one per active race).
type Manager struct {
	hubs map[string]*Hub
	mu   sync.RWMutex
	rdb  *redis.Client
}

func NewManager(rdb *redis.Client) *Manager {
	return &Manager{
		hubs: make(map[string]*Hub),
		rdb:  rdb,
	}
}

// GetOrCreateHub returns the Hub for a race, creating it if necessary.
func (m *Manager) GetOrCreateHub(raceID string) *Hub {
	m.mu.RLock()
	if h, ok := m.hubs[raceID]; ok {
		m.mu.RUnlock()
		return h
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if h, ok := m.hubs[raceID]; ok {
		return h
	}

	ctx, cancel := context.WithCancel(context.Background())
	h := &Hub{
		raceID:    raceID,
		clients:   make(map[*Client]bool),
		broadcast: make(chan []byte, 256),
		join:      make(chan *Client, 64),
		leave:     make(chan *Client, 64),
		rdb:       m.rdb,
		cancel:    cancel,
	}

	go h.run(ctx)
	go h.subscribeRedis(ctx)
	m.hubs[raceID] = h
	return h
}

func (h *Hub) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.join:
			h.mu.Lock()
			h.clients[c] = true
			h.mu.Unlock()
			log.Debug().Str("race", h.raceID).Str("user", c.userID).Msg("ws client joined")
		case c := <-h.leave:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			h.mu.Unlock()
			log.Debug().Str("race", h.raceID).Str("user", c.userID).Msg("ws client left")
		case msg := <-h.broadcast:
			h.mu.RLock()
			for c := range h.clients {
				select {
				case c.send <- msg:
				default:
					// slow client: drop message, don't block hub
				}
			}
			h.mu.RUnlock()
		}
	}
}

// subscribeRedis listens to Redis Pub/Sub for cross-instance broadcast.
func (h *Hub) subscribeRedis(ctx context.Context) {
	ch := "pubsub:race:" + h.raceID
	sub := h.rdb.Subscribe(ctx, ch)
	defer sub.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-sub.Channel():
			if !ok {
				return
			}
			h.broadcast <- []byte(msg.Payload)
		}
	}
}

// Publish sends a message to all clients in this race via Redis Pub/Sub.
// This ensures all API server instances receive the broadcast.
func (h *Hub) Publish(ctx context.Context, msg *Message) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return h.rdb.Publish(ctx, "pubsub:race:"+h.raceID, string(b)).Err()
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ServeWS upgrades an HTTP connection to WebSocket and registers the client.
func (m *Manager) ServeWS(w http.ResponseWriter, r *http.Request, raceID, userID string) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Error().Err(err).Msg("ws upgrade failed")
		return
	}

	hub := m.GetOrCreateHub(raceID)
	client := &Client{
		hub:    hub,
		conn:   conn,
		send:   make(chan []byte, 128),
		userID: userID,
		raceID: raceID,
		config: ClientConfig{
			Enabled:            true,
			RankingIntervalSec: 15,
			FactionAlert:       true,
		},
	}

	hub.join <- client

	go client.writePump()
	go client.readPump()
}

// readPump handles incoming messages from the client (config updates, pings).
func (c *Client) readPump() {
	defer func() {
		c.hub.leave <- c
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Debug().Err(err).Str("user", c.userID).Msg("ws read error")
			}
			break
		}

		// Handle config update from client
		var m Message
		if err := json.Unmarshal(msg, &m); err == nil && m.Type == "config" {
			if b, err := json.Marshal(m.Payload); err == nil {
				json.Unmarshal(b, &c.config)
			}
		}
	}
}

// writePump sends outgoing messages to the client.
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if !c.config.Enabled {
				continue // 用戶停用推播，靜默丟棄
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
