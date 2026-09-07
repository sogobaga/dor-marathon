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

	"github.com/dor/api/internal/reqip"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = 50 * time.Second
	maxMessageSize = 4096

	// hubIdleTimeout：Hub 的最後一位 client 離線後，若 60 秒內沒有新 client 加入，就回收整顆
	// Hub（收掉 goroutine 與 Redis 訂閱）。H7 資安修補——原本 Hub 一旦建立就永遠留著，任意
	// raceID 都能無限製造「永不回收」的 goroutine＋Redis 訂閱，是可放大的資源耗盡缺口。
	hubIdleTimeout = 60 * time.Second

	// maxTotalConns／maxPerIPConn：WS 連線數上限（H7 資安修補）。未認證/低成本即可開一條長連線，
	// 沒有上限時單一來源就能把伺服器連線資源（fd、goroutine、記憶體）耗盡。超過全域上限回 503
	// （服務層級過載），超過單一 IP 上限回 429（這個來源自己濫用）。
	//
	// maxPerIPConn（2026-09-07 audit 調整 20→60）：CF-Connecting-IP 對行動網路 CGNAT、或同一
	// 場地/賽事 WiFi 出口的一大群真實使用者會回報成同一個位址——每人至少開一條 /ws/site，
	// 追蹤中的賽事再加一條 /ws/race，20 太容易被賽事現場（本 app 的核心流量情境）的一小群
	// 合法使用者集體觸頂而被 429。maxTotalConns=2000 留了充足空間，拉高單 IP 上限風險很低。
	maxTotalConns = 2000
	maxPerIPConn  = 60
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

	// data_updated 專用欄位（全站快取失效推播）。
	Topic         string   `json:"topic,omitempty"`
	TargetUserIDs []string `json:"target_user_ids,omitempty"`
}

// ClientConfig stores the user's real-time preferences.
type ClientConfig struct {
	Enabled            bool `json:"enabled"`
	RankingIntervalSec int  `json:"ranking_interval_sec"`
	FactionAlert       bool `json:"faction_alert"`
}

// Client represents a single WebSocket connection.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	userID string
	raceID string
	ip     string // 連線來源 IP（reqip.ClientIP），供 Manager 連線數配額回收用
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
	leave     chan *Client
	rdb       *redis.Client
	cancel    context.CancelFunc
	manager   *Manager

	// closed／idleTimer 皆由 h.mu 保護（H7 閒置回收，見 hubIdleTimeout 與 tryJoin/tryShutdown）：
	//   closed    ＝true 代表這顆 Hub 正在／已經被回收，不再接受新 join（呼叫端應重新
	//              GetOrCreateHub 拿一顆新的，見 ServeWS 的重試迴圈）。
	//   idleTimer ＝目前排定中的回收計時器；有新成員加入時取消（nil）。
	// tryJoin 與 tryShutdown 都先搶 h.mu 才動作，兩者互斥，不會有「剛好同時」的競態視窗
	// （見各自函式註解）。
	closed    bool
	idleTimer *time.Timer
}

// Manager manages all Hubs (one per active race).
type Manager struct {
	hubs map[string]*Hub
	mu   sync.RWMutex
	rdb  *redis.Client

	raceCache *raceExistCache // H7：/ws/race 建立 Hub 前先確認 raceID 真的存在，見 raceexists.go

	// connMu／totalConn／perIPConn：全域＋單一 IP 併發 WS 連線數配額（H7），見 reserveConn/releaseConn。
	connMu    sync.Mutex
	totalConn int
	perIPConn map[string]int
}

func NewManager(rdb *redis.Client, pool pgxQueryer) *Manager {
	return &Manager{
		hubs:      make(map[string]*Hub),
		rdb:       rdb,
		raceCache: newRaceExistCache(pool),
		perIPConn: make(map[string]int),
	}
}

// RaceExists 回報 raceID 是否為真實存在的賽事（帶 TTL 快取，見 raceexists.go）。
// /ws/race/{raceID} 在建立 Hub 前呼叫，未知 raceID 一律 404、不建立 Hub（H7 資安修補）。
func (m *Manager) RaceExists(ctx context.Context, raceID string) bool {
	return m.raceCache.Exists(ctx, raceID)
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
		leave:     make(chan *Client, 64),
		rdb:       m.rdb,
		cancel:    cancel,
		manager:   m,
	}

	go h.run(ctx)
	go h.subscribeRedis(ctx)
	m.hubs[raceID] = h
	return h
}

// removeHub 把 hub 從 m.hubs 移除——只在「目前登記的仍是同一顆 hub 實例」時才刪除，
// 避免與 GetOrCreateHub 之間的競態把「剛建立、取代掉舊實例」的新 hub 誤刪（H7 閒置回收）。
func (m *Manager) removeHub(raceID string, h *Hub) {
	m.mu.Lock()
	if cur, ok := m.hubs[raceID]; ok && cur == h {
		delete(m.hubs, raceID)
	}
	m.mu.Unlock()
}

// reserveConn 嘗試佔用一個連線配額；ok=false 時 status 是應回給呼叫端的 HTTP 狀態碼
// （全域滿載＝503，單一 IP 超額＝429，見 maxTotalConns/maxPerIPConn 常數註解）。
func (m *Manager) reserveConn(ip string) (ok bool, status int) {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if m.totalConn >= maxTotalConns {
		return false, http.StatusServiceUnavailable
	}
	if m.perIPConn[ip] >= maxPerIPConn {
		return false, http.StatusTooManyRequests
	}
	m.totalConn++
	m.perIPConn[ip]++
	return true, 0
}

// releaseConn 歸還 reserveConn 佔用的配額；必須與每一次成功的 reserveConn 恰好配對一次
// （見 ServeWS 所有回傳路徑與 readPump 的 defer）。
func (m *Manager) releaseConn(ip string) {
	m.connMu.Lock()
	defer m.connMu.Unlock()
	if m.totalConn > 0 {
		m.totalConn--
	}
	if n := m.perIPConn[ip]; n > 0 {
		if n == 1 {
			delete(m.perIPConn, ip)
		} else {
			m.perIPConn[ip] = n - 1
		}
	}
}

func (h *Hub) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case c := <-h.leave:
			h.mu.Lock()
			if _, ok := h.clients[c]; ok {
				delete(h.clients, c)
				close(c.send)
			}
			if len(h.clients) == 0 && !h.closed && h.idleTimer == nil {
				// 最後一位離線：排定 60 秒後回收（tryJoin 若有新成員加入會取消，見該函式）。
				h.idleTimer = time.AfterFunc(hubIdleTimeout, h.tryShutdown)
			}
			h.mu.Unlock()
			log.Debug().Str("race", h.raceID).Str("user", c.userID).Msg("ws client left")
		case msg := <-h.broadcast:
			h.deliver(msg)
		}
	}
}

// deliver 把一則已序列化的訊息送給這個 Hub 底下符合條件的 client。
// H7 資安修補：伺服器端先依 target_user_ids 過濾收件人，不再只靠前端「收到後自己比對再決定
// 要不要顯示」——後者代表 session_revoked／帶個資的 data_updated payload 實際上會送到同一
// race/global 頻道上的「所有」連線，只是別人的前端選擇不處理。target_user_ids 為空／未帶
// 時維持原行為＝全體廣播，wire protocol 不變。
func (h *Hub) deliver(msg []byte) {
	var envelope struct {
		TargetUserIDs []string `json:"target_user_ids,omitempty"`
	}
	var targets map[string]bool
	if err := json.Unmarshal(msg, &envelope); err == nil && len(envelope.TargetUserIDs) > 0 {
		targets = make(map[string]bool, len(envelope.TargetUserIDs))
		for _, id := range envelope.TargetUserIDs {
			targets[id] = true
		}
	}

	h.mu.RLock()
	defer h.mu.RUnlock()
	for c := range h.clients {
		if targets != nil && !targets[c.userID] {
			continue
		}
		select {
		case c.send <- msg:
		default:
			// slow client: drop message, don't block hub
		}
	}
}

// tryJoin 嘗試把 client 掛進這個 Hub；回傳 false 代表這顆 Hub 剛好在同一瞬間被閒置回收關閉
// （h.closed），呼叫端（ServeWS）應該重新呼叫 GetOrCreateHub 拿一顆新的再試一次——這個 Hub
// 已經（或即將）呼叫 h.cancel() 收工，join 進去也不會被任何人處理。
//
// 與 tryShutdown 互斥於同一把 h.mu：兩者誰先搶到鎖誰就定案，不存在「join 訊息已送出但
// Hub 已經收工、從此石沉大海」的競態視窗（原本用 channel 傳遞 join 事件時才有這個問題）。
func (h *Hub) tryJoin(c *Client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return false
	}
	h.clients[c] = true
	if h.idleTimer != nil {
		h.idleTimer.Stop()
		h.idleTimer = nil
	}
	log.Debug().Str("race", h.raceID).Str("user", c.userID).Msg("ws client joined")
	return true
}

// tryShutdown 是 hubIdleTimeout 到期後的回收動作：再次確認「現在仍然是空的」才真正關閉
// （避免計時器到期那一刻剛好有人 tryJoin 進來，兩邊靠 h.mu 互斥，見 tryJoin 註解）。
func (h *Hub) tryShutdown() {
	h.mu.Lock()
	if len(h.clients) != 0 || h.closed {
		h.idleTimer = nil // 重置，讓「下次真的變空」時能再排一次
		h.mu.Unlock()
		return
	}
	h.closed = true
	h.mu.Unlock()

	h.manager.removeHub(h.raceID, h)
	h.cancel() // 收掉 run()／subscribeRedis() 兩個 goroutine 與 Redis 訂閱
	log.Debug().Str("race", h.raceID).Msg("ws hub idle timeout, removed")
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

// PublishData 廣播「資料已更新」通知（全站頻道，raceID 固定為 "global"）。
// topic：races | dashboard | personal_tasks | explore | settings。
// targetUserIDs 為 nil/空＝全體廣播；非空＝伺服器端＋前端皆依 target_user_ids 過濾只處理自己的
// （見 Hub.deliver）。Fire-and-forget：錯誤僅記錄，不回傳、不中斷呼叫端流程。
func (m *Manager) PublishData(ctx context.Context, topic string, targetUserIDs []string) {
	hub := m.GetOrCreateHub("global")
	if err := hub.Publish(ctx, &Message{
		Type:          "data_updated",
		Topic:         topic,
		TargetUserIDs: targetUserIDs,
	}); err != nil {
		log.Error().Err(err).Str("topic", topic).Msg("publish data_updated failed")
	}
}

// PublishSessionRevoked 廣播「單一登入」踢除通知：某帳號有新登入發生，通知該帳號其餘
// 連線（舊裝置）session 已被取代。前端收到後應主動登出（refresh 也會因 epoch 不符而 401）。
// Fire-and-forget：錯誤僅記錄，不回傳、不中斷呼叫端（登入）流程。
func (m *Manager) PublishSessionRevoked(ctx context.Context, userID string, epoch int) {
	hub := m.GetOrCreateHub("global")
	if err := hub.Publish(ctx, &Message{
		Type:          "session_revoked",
		Payload:       map[string]any{"epoch": epoch},
		TargetUserIDs: []string{userID},
	}); err != nil {
		log.Error().Err(err).Str("user", userID).Msg("publish session_revoked failed")
	}
}

// ClientCount returns the number of connected clients.
func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ServeWS upgrades an HTTP connection to WebSocket and registers the client.
// H7 資安修補：先套連線數配額（reserveConn，全域/單一 IP 上限），再用 tryJoin 掛進 Hub
// （若剛好撞上該 Hub 被閒置回收，重新拿一顆新的再試，見 tryJoin 註解）。
func (m *Manager) ServeWS(w http.ResponseWriter, r *http.Request, raceID, userID string) {
	ip := reqip.ClientIP(r)
	if ok, status := m.reserveConn(ip); !ok {
		http.Error(w, "too many connections", status)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		m.releaseConn(ip)
		log.Error().Err(err).Msg("ws upgrade failed")
		return
	}

	client := &Client{
		conn:   conn,
		send:   make(chan []byte, 128),
		userID: userID,
		raceID: raceID,
		ip:     ip,
		config: ClientConfig{
			Enabled:            true,
			RankingIntervalSec: 15,
			FactionAlert:       true,
		},
	}

	var hub *Hub
	for i := 0; i < 3; i++ { // 正常情況第一次就成功；重試只為了 tryJoin 撞上 GC 那極窄的競態視窗
		h := m.GetOrCreateHub(raceID)
		if h.tryJoin(client) {
			hub = h
			break
		}
	}
	if hub == nil {
		m.releaseConn(ip)
		conn.Close()
		return
	}
	client.hub = hub

	go client.writePump()
	go client.readPump()
}

// readPump handles incoming messages from the client (config updates, pings).
func (c *Client) readPump() {
	defer func() {
		c.hub.leave <- c
		c.hub.manager.releaseConn(c.ip)
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
