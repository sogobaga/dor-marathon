package realtime

// 2026-09-10 事件驅動踢除（見 hub.go 的 kickChannel／Manager.KickUser／subscribeKicks，以及
// auth.Service.PublishKick 的完整說明）。這裡只測 KickUser 本身「找到並通知正確的 client」這條
// 純邏輯——不起真的 Redis（subscribeKicks 的訂閱迴圈本身沒有可斷言的行為，需要真的 Redis 才能
// 整合測試，不在本檔範圍）、也不需要真的 websocket 連線：Client.kick 只是一個 channel，
// kickUser／KickUser 兩層都只送信號、不碰 c.conn（見 Client.kick 欄位註解——實際關閉動作留給
// writePump 自己的 goroutine，這裡直接組出裸的 *Client／*Hub／*Manager 就能驗證信號有沒有送對人，
// 不需要任何 fake conn 測試scaffolding）。

import "testing"

func newTestManagerWithHub(raceID string) (*Manager, *Hub) {
	h := &Hub{raceID: raceID, clients: make(map[*Client]bool)}
	m := &Manager{hubs: map[string]*Hub{raceID: h}, perIPConn: make(map[string]int)}
	return m, h
}

func TestKickUser_SignalsOnlyMatchingClient(t *testing.T) {
	m, h := newTestManagerWithHub("global")

	target := &Client{userID: "u1", kick: make(chan struct{}, 1)}
	other := &Client{userID: "u2", kick: make(chan struct{}, 1)}
	h.clients[target] = true
	h.clients[other] = true

	m.KickUser("u1")

	select {
	case <-target.kick:
	default:
		t.Fatal("expected the matching client (u1) to receive a kick signal")
	}
	select {
	case <-other.kick:
		t.Fatal("did not expect the non-matching client (u2) to receive a kick signal")
	default:
	}
}

// TestKickUser_AcrossMultipleHubs 驗證 KickUser 會走訪「全部」Hub（一個使用者的 /ws/race 與
// /ws/site 連線落在不同 raceID 的 Hub 上），不是只看某一顆。
func TestKickUser_AcrossMultipleHubs(t *testing.T) {
	raceHub := &Hub{raceID: "race-123", clients: make(map[*Client]bool)}
	globalHub := &Hub{raceID: "global", clients: make(map[*Client]bool)}
	m := &Manager{
		hubs:      map[string]*Hub{"race-123": raceHub, "global": globalHub},
		perIPConn: make(map[string]int),
	}

	onRace := &Client{userID: "u1", kick: make(chan struct{}, 1)}
	onGlobal := &Client{userID: "u1", kick: make(chan struct{}, 1)}
	raceHub.clients[onRace] = true
	globalHub.clients[onGlobal] = true

	m.KickUser("u1")

	select {
	case <-onRace.kick:
	default:
		t.Error("expected the race-hub client to receive a kick signal")
	}
	select {
	case <-onGlobal.kick:
	default:
		t.Error("expected the global-hub client to receive a kick signal")
	}
}

// TestKickUser_MultipleClientsSameUser 同一使用者在同一顆 Hub 底下有多條連線（多分頁/多裝置）
// 時，全部都要收到信號，不是只有第一個命中的。
func TestKickUser_MultipleClientsSameUser(t *testing.T) {
	m, h := newTestManagerWithHub("global")

	c1 := &Client{userID: "u1", kick: make(chan struct{}, 1)}
	c2 := &Client{userID: "u1", kick: make(chan struct{}, 1)}
	h.clients[c1] = true
	h.clients[c2] = true

	m.KickUser("u1")

	for i, c := range []*Client{c1, c2} {
		select {
		case <-c.kick:
		default:
			t.Errorf("client %d: expected a kick signal", i)
		}
	}
}

func TestKickUser_EmptyUserIDIsNoop(t *testing.T) {
	m, h := newTestManagerWithHub("global")
	c := &Client{userID: "", kick: make(chan struct{}, 1)}
	h.clients[c] = true

	m.KickUser("") // 不該 panic，也不該誤傷 userID 為空字串的（理論上不會出現的）client

	select {
	case <-c.kick:
		t.Error("did not expect a kick signal when userID is empty")
	default:
	}
}

// TestKickUser_NoHubsIsSafe 走訪 nil/空 hubs map 時不能 panic（例如：所有連線都已斷線、
// Hub 已被閒置回收，只剩下 Manager 本身）。
func TestKickUser_NoHubsIsSafe(t *testing.T) {
	m := &Manager{hubs: map[string]*Hub{}, perIPConn: make(map[string]int)}
	m.KickUser("u1") // 不應 panic
}

// TestHub_KickUser_DoesNotDoubleBufferSignal 驗證 kick channel 是 buffered 1、non-blocking
// send（見 Hub.kickUser 的 select/default）：已經有一個待處理信號時，第二次呼叫不會阻塞
// （若退化成 blocking send，這個測試會直接 hang 到 go test 逾時，本身就是斷言）。
func TestHub_KickUser_DoesNotDoubleBufferSignal(t *testing.T) {
	h := &Hub{raceID: "global", clients: make(map[*Client]bool)}
	c := &Client{userID: "u1", kick: make(chan struct{}, 1)}
	h.clients[c] = true

	h.kickUser("u1")
	h.kickUser("u1") // channel 已滿，靠 select/default 不阻塞——沒有 default 分支的話這行會卡死

	select {
	case <-c.kick:
	default:
		t.Fatal("expected at least one buffered kick signal")
	}
}
