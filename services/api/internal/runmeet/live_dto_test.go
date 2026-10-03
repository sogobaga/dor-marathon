package runmeet

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/dor/api/internal/runmeet/live"
)

// live_dto_test.go：詳情 DTO 的 live 區塊（契約 §2）——只在 MemberDetailView，PublicDetailView 結構上沒有。

func liveTestMeet() meetRow {
	lat, lng := 25.033, 121.565
	return meetRow{
		ID: "0123abcd-4567-89ab-cdef-0123456789ab", OwnerID: "owner-1",
		Title: "河濱團練", MeetAt: time.Date(2026, 10, 10, 12, 0, 0, 0, taipei),
		Region: "臺北市・大安區", PlaceLabel: "大安森林公園", Lat: &lat, Lng: &lng,
		Capacity: 10, ImageURLs: []string{}, ImageLimit: 1, MemberCount: 1, Status: StatusOpen, ShowCover: true,
	}
}

func marshalMap(t *testing.T, v any) map[string]json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// 結構層保證：PublicDetailView（含內嵌的 detailBase／CardView）沒有任何 live 欄位。
func TestPublicDetailViewStructurallyHasNoLiveField(t *testing.T) {
	ty := reflect.TypeOf(PublicDetailView{})
	var walk func(reflect.Type)
	walk = func(ty reflect.Type) {
		for i := 0; i < ty.NumField(); i++ {
			f := ty.Field(i)
			if tag := f.Tag.Get("json"); tag == "live" || tag == "live,omitempty" || f.Name == "Live" {
				t.Errorf("%s has a live field (%q) — only MemberDetailView may carry it", ty, tag)
			}
			if f.Anonymous && f.Type.Kind() == reflect.Struct {
				walk(f.Type)
			}
		}
	}
	walk(ty)
	if _, ok := reflect.TypeOf(MemberDetailView{}).FieldByName("Live"); !ok {
		t.Fatal("MemberDetailView must carry the Live block")
	}
}

func TestBuildDetailLiveBlockOnlyForMembers(t *testing.T) {
	h := &Handler{}
	m := liveTestMeet()

	// 非成員：JSON 不含 "live"、也不含成員層三欄
	stranger := marshalMap(t, h.buildDetail(&m, "stranger", false))
	if _, has := stranger["live"]; has {
		t.Fatal("non-member detail must not contain the live block")
	}

	// 發起人（MemberDetailView）：live 一律存在，預設 enabled=false（fail-closed）
	owner := marshalMap(t, h.buildDetail(&m, "owner-1", false))
	raw, has := owner["live"]
	if !has {
		t.Fatal("member detail must contain the live block")
	}
	var blk struct {
		Enabled      bool      `json:"enabled"`
		OpensAt      time.Time `json:"opens_at"`
		ClosesAt     time.Time `json:"closes_at"`
		ServerNow    time.Time `json:"server_now"`
		PresenceOnly bool      `json:"presence_only"`
	}
	if err := json.Unmarshal(raw, &blk); err != nil {
		t.Fatal(err)
	}
	if blk.Enabled {
		t.Fatal("buildDetail has no way to know the entry state — must default to enabled=false")
	}
	if !blk.OpensAt.Equal(m.MeetAt.Add(-30*time.Minute)) || !blk.ClosesAt.Equal(m.MeetAt.Add(3*time.Hour+30*time.Minute)) {
		t.Fatalf("default window = [%v, %v)", blk.OpensAt, blk.ClosesAt)
	}
	if blk.ServerNow.IsZero() || time.Since(blk.ServerNow) > time.Minute {
		t.Fatalf("server_now = %v", blk.ServerNow)
	}
	// 契約規定的 key 一個不多、一個不少
	var keys map[string]json.RawMessage
	_ = json.Unmarshal(raw, &keys)
	for _, k := range []string{"enabled", "opens_at", "closes_at", "server_now", "presence_only"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("live block lacks %q", k)
		}
	}
	if len(keys) != 5 {
		t.Errorf("live block has %d keys, want exactly 5: %s", len(keys), raw)
	}

	// 後台視角（isAdmin=true 的 MemberDetailView）同樣是成員層
	admin := marshalMap(t, h.buildDetail(&m, "admin-user", true))
	if _, has := admin["live"]; !has {
		t.Fatal("admin (member-layer) detail carries the live block too")
	}
}

func TestBuildLiveInfoWindowAndPresenceOnly(t *testing.T) {
	m := liveTestMeet()
	now := time.Date(2026, 10, 10, 11, 0, 0, 0, taipei)
	s := live.DefaultSettings()

	li := buildLiveInfo(&m, s, true, now)
	if !li.Enabled || li.PresenceOnly || !li.ServerNow.Equal(now) {
		t.Fatalf("%+v", li)
	}
	if !li.OpensAt.Equal(time.Date(2026, 10, 10, 11, 30, 0, 0, taipei)) ||
		!li.ClosesAt.Equal(time.Date(2026, 10, 10, 15, 30, 0, 0, taipei)) {
		t.Fatalf("ends_at NULL window = [%v, %v)", li.OpensAt, li.ClosesAt)
	}

	ends := time.Date(2026, 10, 10, 14, 0, 0, 0, taipei)
	m.EndsAt = &ends
	li = buildLiveInfo(&m, s, false, now)
	if li.Enabled || !li.ClosesAt.Equal(time.Date(2026, 10, 10, 14, 30, 0, 0, taipei)) {
		t.Fatalf("ends_at window = %+v", li)
	}

	m.NoLocation = true
	if li = buildLiveInfo(&m, s, true, now); !li.PresenceOnly {
		t.Fatal("no_location meet must report presence_only=true")
	}

	li = buildLiveInfo(&m, live.Settings{PreMinutes: 10, DefaultHours: 2, GraceMinutes: 0, Max: 50, EntryState: "open"}, true, now)
	if !li.OpensAt.Equal(time.Date(2026, 10, 10, 11, 50, 0, 0, taipei)) || !li.ClosesAt.Equal(ends) {
		t.Fatalf("custom settings window = [%v, %v)", li.OpensAt, li.ClosesAt)
	}
}

// detailView：依觀看者的 live 入口覆寫 enabled、時窗吃現行設定；非成員視角原樣不動。
func TestDetailViewSetsEnabledFromEntryAndSettings(t *testing.T) {
	m := liveTestMeet()
	h := &Handler{liveSettingsFn: func(context.Context) live.Settings {
		s := live.DefaultSettings()
		s.EntryState, s.Whitelist, s.PreMinutes = "whitelist", "vip@x.com", 60
		return s
	}}
	ctxFor := func(email string, super bool) context.Context {
		return withEntryFlags(context.Background(), email, "", super, false)
	}
	liveOf := func(v any) *LiveInfo {
		mv, ok := v.(MemberDetailView)
		if !ok {
			t.Fatalf("not a MemberDetailView: %T", v)
		}
		return mv.Live
	}

	if li := liveOf(h.detailView(ctxFor("vip@x.com", false), &m, "owner-1")); li == nil || !li.Enabled {
		t.Fatalf("whitelisted member: %+v", li)
	} else if !li.OpensAt.Equal(m.MeetAt.Add(-60 * time.Minute)) {
		t.Fatalf("pre_minutes from settings not applied: opens_at = %v", li.OpensAt)
	}
	if li := liveOf(h.detailView(ctxFor("nobody@x.com", false), &m, "owner-1")); li == nil || li.Enabled {
		t.Fatalf("non-whitelisted member must see enabled=false: %+v", li)
	}
	if li := liveOf(h.detailView(ctxFor("nobody@x.com", true), &m, "owner-1")); li == nil || !li.Enabled {
		t.Fatalf("super admin must be enabled: %+v", li)
	}

	// 非成員：回傳的就是 PublicDetailView，沒有 live
	if _, ok := h.detailView(ctxFor("vip@x.com", false), &m, "stranger").(PublicDetailView); !ok {
		t.Fatal("non-member must still get a PublicDetailView")
	}

	// hidden：連超管都 enabled=false（與緊急關閉一致）
	h.liveSettingsFn = func(context.Context) live.Settings {
		s := live.DefaultSettings()
		s.EntryState = "hidden"
		return s
	}
	if li := liveOf(h.detailView(ctxFor("vip@x.com", true), &m, "owner-1")); li == nil || li.Enabled {
		t.Fatalf("hidden must disable the entry for everyone, super admin included: %+v", li)
	}
}
