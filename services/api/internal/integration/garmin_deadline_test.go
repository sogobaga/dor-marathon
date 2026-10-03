package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
)

// pipeListener：以 net.Pipe 取代本機 TCP（本機沙盒會改寫 loopback HTTP；這裡的連線完全在記憶體內，但走真正的
// http.Server：ReadTimeout、中介層鏈、ResponseController 都是真的）。
type pipeListener struct {
	conns chan net.Conn
	done  chan struct{}
	once  sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, errors.New("listener closed")
	}
}
func (l *pipeListener) Close() error   { l.once.Do(func() { close(l.done) }); return nil }
func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }
func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	c1, c2 := net.Pipe()
	select {
	case l.conns <- c2:
		return c1, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }

// startPipeServer 起一個真的 http.Server（連線走記憶體內 net.Pipe）：中介層鏈與正式環境相同的 chi Compress／Timeout，
// webhook 掛在 /api/v1/integrations/garmin，另有一個不放寬讀取期限的對照路由 /control。
func startPipeServer(t *testing.T, tg *testGarmin, readTimeout time.Duration) *http.Client {
	t.Helper()
	r := chi.NewRouter()
	r.Use(chimiddleware.Compress(5))
	r.Use(chimiddleware.Timeout(30 * time.Second))
	r.Mount("/api/v1/integrations/garmin", tg.h.Router())
	r.Post("/control", func(w http.ResponseWriter, req *http.Request) { // 對照組：不放寬讀取期限
		if _, err := io.ReadAll(req.Body); err != nil {
			w.WriteHeader(http.StatusRequestTimeout)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	l := newPipeListener()
	srv := &http.Server{Handler: r, ReadTimeout: readTimeout}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close(); l.Close() })
	return &http.Client{Transport: &http.Transport{DialContext: l.dial, DisableKeepAlives: true}, Timeout: 60 * time.Second}
}

// 全域 http.Server.ReadTimeout（正式環境 15 秒）對慢速／大 body 太短：webhook 以 http.ResponseController 把讀取期限
// 放寬到 25 秒。這個測試用真的 http.Server（ReadTimeout=1s）＋真的中介層鏈驗證：對照組（一般 handler）在 1 秒後
// 讀取失敗；webhook 在同樣設定下可以慢慢收完 body（總耗時 >1s）並回精確 200。
func TestWebhook_ReadDeadlineExtendedThroughMiddlewareChain(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	client := startPipeServer(t, tg, time.Second)

	body, _ := json.Marshal(synthPush(synthActivity(tUser1, "DL-1", time.Now().Add(-time.Hour).Unix())))
	slowPost := func(path string) (int, time.Duration, error) {
		pr, pw := io.Pipe()
		go func() {
			third := len(body) / 3
			for _, part := range [][]byte{body[:third], body[third : 2*third], body[2*third:]} {
				time.Sleep(700 * time.Millisecond) // 總共 ~2.1 秒 > ReadTimeout 1 秒
				if _, err := pw.Write(part); err != nil {
					return
				}
			}
			pw.Close()
		}()
		req, _ := http.NewRequest(http.MethodPost, "http://pipe"+path, pr) // 無 Content-Length → chunked
		start := time.Now()
		resp, err := client.Do(req)
		if err != nil {
			return 0, time.Since(start), err
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		return resp.StatusCode, time.Since(start), nil
	}

	code, el, err := slowPost("/control")
	if err == nil && code == http.StatusOK {
		t.Fatalf("control: the server ReadTimeout should have cut a %s upload (code=%d)", el.Round(time.Millisecond), code)
	}
	code, el, err = slowPost("/api/v1/integrations/garmin/webhook/" + testGarminToken + "/activities")
	if err != nil || code != http.StatusOK {
		t.Fatalf("webhook slow upload: code=%d err=%v after %s (ResponseController must extend the read deadline through the middleware chain)", code, err, el)
	}
	if el < 1500*time.Millisecond {
		t.Fatalf("the upload was supposed to take >1.5s, took %s", el)
	}
	if tg.store.count() != 1 {
		t.Fatalf("event must be persisted, got %d", tg.store.count())
	}
	_ = strings.TrimSpace
}

// 經過真的 http.Server＋中介層鏈的大 body：10 MiB 與 16 MiB 收得下（精確 200，空 body）；超過 16 MiB 回 413。
func TestWebhook_RealServer_LargeBodies(t *testing.T) {
	tg := newTestGarmin(t, nil)
	tg.store.addUser(tUser1, tDor1)
	client := startPipeServer(t, tg, 20*time.Second)
	for _, tc := range []struct {
		name string
		size int
		want int
	}{{"10MiB", 10 << 20, 200}, {"16MiB", 16 << 20, 200}, {"16MiB+1", 16<<20 + 1, 413}} {
		payload := synthBigPush(t, tc.size, tUser1)
		start := time.Now()
		resp, err := client.Post("http://pipe/api/v1/integrations/garmin/webhook/"+testGarminToken+"/activities", "application/json", bytes.NewReader(payload))
		if err != nil {
			// 413 時伺服器會關閉連線；用戶端可能在寫完 body 之前就收到回應而報錯，只有 413 這一格容許
			if tc.want == 413 {
				t.Logf("%s: transport error after early 413 (acceptable): %v", tc.name, err)
				continue
			}
			t.Fatalf("%s: %v", tc.name, err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.want || (tc.want == 200 && len(b) != 0) {
			t.Fatalf("%s: status=%d body=%q, want %d", tc.name, resp.StatusCode, b, tc.want)
		}
		t.Logf("%s: %d in %s", tc.name, resp.StatusCode, time.Since(start).Round(time.Millisecond))
	}
}
