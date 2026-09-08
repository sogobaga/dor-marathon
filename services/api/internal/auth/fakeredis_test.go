package auth

// fakeRedisServer 是一個極簡的 RESP2 server，只實作本套件測試會用到的三個命令
// （SET／SETNX 走的其實也是 SET ... NX／EXISTS），供「撤銷名單真的有沒有生效」這類需要真的
// 撤銷/讀回一次的測試使用——sandbox 環境沒有 Docker/miniredis 可用（見本次修法的調查紀錄），
// 但 go-redis client 在沒有設定 DB/ClientName/tracking 時，除了握手用的 HELLO 之外不會送出其他
// 初始化指令，實作到「HELLO 回錯誤（讓 client 照著官方文件的相容路徑退回 RESP2）+ SET + EXISTS」
// 就足以撐起 Service.Logout／ValidateAccessToken 之間的整合測試，不需要真的引入新的 go.mod 依賴。
//
// 只支援本檔測試會用到的最小子集，不是一般用途的 redis mock——新增測試如果用到其他命令
// （GET/DEL/…）要自己補上對應分支。

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

type fakeRedisServer struct {
	mu   sync.Mutex
	data map[string]string
	ln   net.Listener
}

func startFakeRedisServer(t *testing.T) *fakeRedisServer {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("fakeRedisServer listen: %v", err)
	}
	s := &fakeRedisServer{data: map[string]string{}, ln: ln}
	go s.acceptLoop()
	t.Cleanup(func() { ln.Close() })
	return s
}

func (s *fakeRedisServer) addr() string { return s.ln.Addr().String() }

func (s *fakeRedisServer) acceptLoop() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return // listener closed (test cleanup)
		}
		go s.handleConn(conn)
	}
}

func (s *fakeRedisServer) handleConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	for {
		args, err := readRESPCommand(r)
		if err != nil {
			return
		}
		if len(args) == 0 {
			continue
		}
		resp := s.dispatch(args)
		if _, err := conn.Write(resp); err != nil {
			return
		}
	}
}

// readRESPCommand 讀一條 RESP array-of-bulk-strings 命令（redis client 對 server 送指令永遠是
// 這個形狀，不需要支援其他 RESP 型別）。
func readRESPCommand(r *bufio.Reader) ([]string, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		return nil, err
	}
	line = strings.TrimRight(line, "\r\n")
	if len(line) == 0 || line[0] != '*' {
		return nil, fmt.Errorf("fakeredis: expected array, got %q", line)
	}
	n, err := strconv.Atoi(line[1:])
	if err != nil || n < 0 {
		return nil, fmt.Errorf("fakeredis: bad array len %q", line)
	}
	args := make([]string, 0, n)
	for i := 0; i < n; i++ {
		head, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		head = strings.TrimRight(head, "\r\n")
		if len(head) == 0 || head[0] != '$' {
			return nil, fmt.Errorf("fakeredis: expected bulk string, got %q", head)
		}
		blen, err := strconv.Atoi(head[1:])
		if err != nil || blen < 0 {
			return nil, fmt.Errorf("fakeredis: bad bulk len %q", head)
		}
		buf := make([]byte, blen+2) // +2 trailing \r\n
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}
		args = append(args, string(buf[:blen]))
	}
	return args, nil
}

func respError(msg string) []byte { return []byte("-ERR " + msg + "\r\n") }
func respOK() []byte              { return []byte("+OK\r\n") }
func respNilBulk() []byte         { return []byte("$-1\r\n") }
func respInt(n int) []byte        { return []byte(":" + strconv.Itoa(n) + "\r\n") }

func (s *fakeRedisServer) dispatch(args []string) []byte {
	cmd := strings.ToUpper(args[0])
	switch cmd {
	case "HELLO":
		// 讓 go-redis 的 initConn 判定「這個 server 不支援 HELLO」、退回 RESP2 繼續用（見
		// redis.go initConn 對 isRedisError(initErr) 的處理），是官方文件明訂的相容路徑，
		// 不是山寨行為。
		return respError("unknown command 'HELLO'")
	case "SET":
		return s.handleSet(args[1:])
	case "EXISTS":
		return s.handleExists(args[1:])
	default:
		return respError("unknown command '" + args[0] + "'")
	}
}

// handleSet 支援本檔測試會用到的形狀：SET key value [PX ms|EX sec] [NX]（go-redis 的
// Set()/SetNX() 送出的參數組合，見 string_commands.go）。忽略實際 TTL 生效（測試跑得比任何
// TTL 都快，過期與否不影響斷言），只在乎 NX 語意與值本身。
func (s *fakeRedisServer) handleSet(args []string) []byte {
	if len(args) < 2 {
		return respError("wrong number of arguments for 'set' command")
	}
	key, value := args[0], args[1]
	nx := false
	for _, a := range args[2:] {
		if strings.EqualFold(a, "NX") {
			nx = true
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if nx {
		if _, exists := s.data[key]; exists {
			return respNilBulk() // SET ... NX 失敗 → BoolCmd 解讀成 false
		}
	}
	s.data[key] = value
	return respOK()
}

func (s *fakeRedisServer) handleExists(keys []string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, k := range keys {
		if _, ok := s.data[k]; ok {
			n++
		}
	}
	return respInt(n)
}

// newFakeRedisService 建一個接到 fakeRedisServer 的 Service，供需要「撤銷真的生效／single-use
// 真的擋下第二次」這類整合行為的測試使用（純 fail-open 測試請繼續用 newTestService／
// newUnreachableRedis，不需要真的連得上）。Protocol:2：本檔的 fakeRedisServer 只實作 RESP2，
// 見上方 dispatch 對 HELLO 的處理（無論 Protocol 設什麼，go-redis 都會先送一次 HELLO，這裡靠
// 讓它失敗來觸發官方文件內建的 RESP2 相容退回路徑，Protocol:2 只是額外明示意圖、非必要）。
func newFakeRedisService(t *testing.T) *Service {
	t.Helper()
	srv := startFakeRedisServer(t)
	rdb := redis.NewClient(&redis.Options{
		Addr:         srv.addr(),
		Protocol:     2,
		DialTimeout:  2 * time.Second,
		ReadTimeout:  2 * time.Second,
		WriteTimeout: 2 * time.Second,
	})
	t.Cleanup(func() { rdb.Close() })
	return NewService(nil, rdb, "test-jwt-secret-please-ignore", time.Hour, 720*time.Hour, "")
}
