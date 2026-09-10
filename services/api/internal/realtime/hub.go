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

	// revalidateInterval／closeCodeRevalidateFailed：2026-09-08 audit finding 4——ServeWS 原本
	// 只在連線建立那一刻驗證一次 token，之後這條連線就算 token 過期/被撤銷（登出、密碼被改、
	// 單一登入踢除…）也會一直活著，直到使用者自己重新整理頁面。見 Client.writePump 的重新
	// 驗證迴圈：每 revalidateInterval 重打一次呼叫端注入的 revalidate closure，失敗就用
	// closeCodeRevalidateFailed（RFC 6455 4000-4999 私有區段）主動關閉連線。
	//
	// 2026-09-10：5→15 分鐘——呼叫端已改用 auth.Service.RevalidateAccessTokenNoDB（不查 DB，
	// 只查 Redis 撤銷名單），且「立即生效」的保證已改由 KickUser／subscribeKicks 這條事件驅動
	// 路徑負責（登入/改密碼/撤銷當下透過 Redis Pub/Sub 主動關閉舊連線），這裡退化成單純的
	// backstop（Kick 事件因 Redis 短暫抖動漏送時的最後防線），可以拉長週期、降低成本。
	revalidateInterval        = 15 * time.Minute
	closeCodeRevalidateFailed = 4401

	// kickChannel 是「單一登入強制踢除」的 Redis Pub/Sub 頻道名稱，必須與 auth.KickChannel 的值
	// 完全一致（"user_kick"）——auth 套件已 import 本套件（見 auth.Handler.SetRealtime／
	// PublishSessionRevoked 既有用法），這裡不能反過來 import auth 取常數（會造成 import
	// cycle），故兩邊各自宣告同一個字串常數，純粹靠 Redis 頻道名稱字串本身耦合（與既有的
	// "pubsub:race:"+raceID／PublishData 用固定 raceID "global" 是同一種耦合方式）。
	kickChannel = "user_kick"
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

	// revalidate：2026-09-08 audit finding 4，見 revalidateInterval 的說明與 ServeWS 的參數。
	// 呼叫端（cmd/api/main.go 的 /ws/race、/ws/site handler）注入一個重跑 authSvc.
	// ValidateAccessToken(ctx, token) 的 closure（同一顆連線建立時用的 token）；為 nil 時
	// writePump 整段重新驗證迴圈跳過，行為等同修法前（僅供保守起見，正常呼叫路徑一定會傳）。
	revalidate func(context.Context) error

	// kick：2026-09-10 事件驅動踢除（見 kickChannel／Manager.KickUser／subscribeKicks）用的
	// 信號 channel——buffered 1，non-blocking send（Hub.kickUser 觸發）。刻意不讓觸發端
	// （subscribeKicks 的 goroutine）直接對 c.conn 呼叫 WriteMessage/Close：gorilla/websocket
	// 的 *Conn 不支援併發寫入，目前整個套件只有 writePump 這一個 goroutine 會寫 conn，讓
	// kickUser 也遵守這條規則、只送一個訊號，實際關閉動作仍在 writePump 自己的 select 迴圈裡
	// 完成（與 revalidate 失敗時的關閉走同一段程式碼路徑：4401 close code）。
	kick chan struct{}
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
	m := &Manager{
		hubs:      make(map[string]*Hub),
		rdb:       rdb,
		raceCache: newRaceExistCache(pool),
		perIPConn: make(map[string]int),
	}
	// 2026-09-10 事件驅動踢除：Manager 是行程存活期間唯一一份（main.go 只建一次），這裡直接起一顆
	// 背景 goroutine 訂閱 kickChannel，比照既有各 Hub 的 run()/subscribeRedis() 用 context.
	// Background()——這幾個背景 goroutine 目前都沒有明確的行程級關閉流程，進程結束時一併收工。
	go m.subscribeKicks(context.Background())
	return m
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

// subscribeKicks 訂閱 kickChannel（見該常數與 auth.Service.PublishKick 的完整說明，2026-09-10）：
// 單一登入/改密碼/後台強制登出 事件驅動地即時關閉舊連線，取代讓每條 WS 定期回頭查 DB 才能發現
// 「已被踢」的做法（revalidateInterval 現在只是 backstop，見上方常數說明）。純 Redis pub/sub，
// 不碰 DB；訂閱斷線（Redis 重啟/網路抖動）用簡單的固定退避重連——這條背景連線斷了頂多退化回
// 「依賴 revalidateInterval 這個 backstop」，不影響其他功能，不需要更複雜的重試策略。
func (m *Manager) subscribeKicks(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		sub := m.rdb.Subscribe(ctx, kickChannel)
		// 先用 Receive 確認訂閱真的成功（go-redis 的 Subscribe 本身不會立即回報連線層錯誤）；
		// 失敗（含 Redis 打不通、ctx 取消）才進退避重試，成功後把退避重置。
		if _, err := sub.Receive(ctx); err != nil {
			sub.Close()
			if ctx.Err() != nil {
				return
			}
			log.Warn().Err(err).Msg("realtime: kick channel subscribe failed, retrying")
			time.Sleep(backoff)
			if backoff < maxBackoff {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second

		ch := sub.Channel()
	readLoop:
		for {
			select {
			case <-ctx.Done():
				sub.Close()
				return
			case msg, ok := <-ch:
				if !ok {
					break readLoop // 連線斷線（channel 關閉）：跳出內層迴圈，外層重新訂閱
				}
				log.Debug().Str("user", msg.Payload).Msg("realtime: kick received")
				m.KickUser(msg.Payload)
			}
		}
		sub.Close()
	}
}

// KickUser 立即關閉所有屬於 userID 的 WebSocket 連線（不分 race／global），供 subscribeKicks
// 收到 kickChannel 訊息時呼叫；獨立成方法也方便單元測試直接呼叫、不需要真的起一個 Redis
// pub/sub（見 hub_test.go）。逐一走訪目前所有 Hub，在各自既有的 h.mu 保護下找出 userID 相符的
// client 送出關閉信號——不另外維護一份 userID→client 的全域索引（多一份索引就多一個要跟
// h.clients 保持同步的地方；連線總數上限只有 maxTotalConns=2000，逐一走訪的成本可忽略）。
func (m *Manager) KickUser(userID string) {
	if userID == "" {
		return
	}
	m.mu.RLock()
	hubs := make([]*Hub, 0, len(m.hubs))
	for _, h := range m.hubs {
		hubs = append(hubs, h)
	}
	m.mu.RUnlock()

	for _, h := range hubs {
		h.kickUser(userID)
	}
}

// kickUser 對這個 Hub 底下所有 userID 相符的 client 送出關閉信號（見 Client.kick 欄位註解：
// 實際關閉動作留給各自的 writePump goroutine 做，這裡不直接碰 c.conn）。
func (h *Hub) kickUser(userID string) {
	h.mu.RLock()
	var targets []*Client
	for c := range h.clients {
		if c.userID == userID {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.kick <- struct{}{}:
		default: // 已經有一個待處理的 kick 信號／writePump 正好在處理，不需要疊加
		}
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
// revalidate：2026-09-08 audit finding 4，見 Client.revalidate 欄位與 writePump 的重新驗證
// 迴圈說明；呼叫端（cmd/api/main.go）在連線建立當下已經驗證過一次 token，這裡再傳入一個閉包
// 讓連線存活期間可以定期重打同一個驗證。
func (m *Manager) ServeWS(w http.ResponseWriter, r *http.Request, raceID, userID string, revalidate func(context.Context) error) {
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
		conn:       conn,
		send:       make(chan []byte, 128),
		userID:     userID,
		raceID:     raceID,
		ip:         ip,
		revalidate: revalidate,
		kick:       make(chan struct{}, 1),
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
// 2026-09-08 audit finding 4：新增一個每 revalidateInterval 觸發一次的重新驗證分支——見
// Client.revalidate 欄位與上方常數說明。revalidate 為 nil（理論上不會，ServeWS 的兩個呼叫點都
// 會傳，只是防呆）時 revalidateChan 保持 nil，select 上 nil channel 永遠不會被選中，這個分支
// 整段跳過，行為等同修法前。
func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	defer c.conn.Close()

	var revalidateChan <-chan time.Time
	if c.revalidate != nil {
		revalidateTicker := time.NewTicker(revalidateInterval)
		defer revalidateTicker.Stop()
		revalidateChan = revalidateTicker.C
	}

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
		case <-revalidateChan:
			rctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err := c.revalidate(rctx)
			cancel()
			if err != nil {
				log.Debug().Err(err).Str("user", c.userID).Msg("ws revalidate failed, closing connection")
				c.conn.SetWriteDeadline(time.Now().Add(writeWait))
				c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(closeCodeRevalidateFailed, "token revalidation failed"))
				return
			}
		case <-c.kick:
			// 2026-09-10 事件驅動踢除（見 kickChannel／Manager.KickUser 說明）：與上面 revalidate
			// 失敗走同一個 4401 close code，前端既有的 WS onclose 處理不需要區分這條連線是被
			// 「背景重驗踢的」還是「單一登入/改密碼即時踢的」。
			log.Debug().Str("user", c.userID).Msg("ws client kicked, closing connection")
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			c.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(closeCodeRevalidateFailed, "session revoked"))
			return
		}
	}
}
