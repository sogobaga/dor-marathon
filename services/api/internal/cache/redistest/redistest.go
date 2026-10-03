// Package redistest 測試用的「壞掉的 Redis」：只給 *_test.go 使用（與 net/http/httptest 同類，不會被連結進正式執行檔）。
//
// 為什麼需要：Redis 故障有三種形狀，go-redis 的行為差很多，只測「連不上」會漏掉最痛的那種——
//
//   - Refused    連線被拒（容器停了、埠沒人聽）：撥號立刻失敗，但 go-redis 連線池的撥號重試（5 次 × 100 ms）
//     加上指令重試，實測要 ≈ 1.7 秒才放棄——所以「連線被拒」也不是瞬間失敗，仍需要期限。
//   - Blackhole  接受連線但永遠不回話（網路分區、Redis 卡死／被 SIGSTOP、docker pause）：撥號成功、
//     寫入成功，然後讀取一直卡到 ReadTimeout（go-redis v9.22 預設 5 秒）；實測全新連線 ≈ 5 秒、已建立的連線
//     ≈ 10 秒（指令讀取逾時 5 秒 + 重試在新連線的握手再等 5 秒）。
//   - Proxy      「先正常、後來才卡死」：連線已在連線池裡，Redis 中途停止回應。這是 Blackhole 的
//     變體，專門驗證「已建立的連線」上的讀取逾時（而不只是新連線的握手逾時）。
//
// 全部走 loopback TCP（RESP，不是 HTTP；本機沙盒只會改寫 loopback 的 HTTP 回應）。
package redistest

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// Blackhole 在 127.0.0.1 的隨機埠開一個 TCP 監聽：接受連線、永不讀取、永不回應。回傳位址。
func Blackhole(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("redistest.Blackhole: listen: %v", err)
	}
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // 抓住不放：不 close、不讀、不寫
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range held {
			_ = c.Close()
		}
		mu.Unlock()
	})
	return ln.Addr().String()
}

// Refused 回傳一個「沒有任何東西在聽」的 127.0.0.1 位址（連線會被拒絕）。
func Refused(t testing.TB) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("redistest.Refused: listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// Proxy 把 TCP 轉送到 target（例如 miniredis），直到 Blackhole() 被呼叫：之後所有已建立與新建立的連線都
// 保持開啟，但不再轉送任何位元組（雙向都丟棄）——對 client 而言就是「Redis 卡死了」。
type Proxy struct {
	ln     net.Listener
	target string
	dark   atomic.Bool

	mu    sync.Mutex
	conns []net.Conn
}

// NewProxy 開始轉送到 target。
func NewProxy(t testing.TB, target string) *Proxy {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("redistest.NewProxy: listen: %v", err)
	}
	p := &Proxy{ln: ln, target: target}
	go p.serve()
	t.Cleanup(func() {
		_ = ln.Close()
		p.mu.Lock()
		for _, c := range p.conns {
			_ = c.Close()
		}
		p.mu.Unlock()
	})
	return p
}

// Addr 給 redis.Options.Addr 用。
func (p *Proxy) Addr() string { return p.ln.Addr().String() }

// Blackhole 從現在起不再轉送（連線不關閉）。
func (p *Proxy) Blackhole() { p.dark.Store(true) }

func (p *Proxy) track(c net.Conn) {
	p.mu.Lock()
	p.conns = append(p.conns, c)
	p.mu.Unlock()
}

func (p *Proxy) serve() {
	for {
		c, err := p.ln.Accept()
		if err != nil {
			return
		}
		p.track(c)
		if p.dark.Load() {
			continue // 已經卡死：接受後什麼都不做
		}
		up, err := net.Dial("tcp", p.target)
		if err != nil {
			_ = c.Close()
			continue
		}
		p.track(up)
		go p.pipe(c, up)
		go p.pipe(up, c)
	}
}

func (p *Proxy) pipe(src, dst net.Conn) {
	buf := make([]byte, 32*1024)
	for {
		n, err := src.Read(buf)
		if n > 0 && !p.dark.Load() {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}
