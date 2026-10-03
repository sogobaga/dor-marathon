package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dor/api/internal/auth"
)

func TestTerraDefaultProvidersExcludeGarminAndCoros(t *testing.T) {
	h := NewTerraHandler(nil, TerraConfig{}, nil)
	got := strings.Join(h.cfg.Providers, ",")
	if got != "POLAR,SUUNTO,WAHOO" {
		t.Fatalf("default providers = %s, want POLAR,SUUNTO,WAHOO", got)
	}
	if p := ParseTerraProviders(""); p != nil {
		t.Fatalf("ParseTerraProviders(\"\") = %v, want nil (→ defaults without garmin/coros)", p)
	}
	// 明確列出時仍照列（擁有者在切換點前可保留 GARMIN）
	h = NewTerraHandler(nil, TerraConfig{Providers: ParseTerraProviders("garmin,polar")}, nil)
	if strings.Join(h.cfg.Providers, ",") != "GARMIN,POLAR" {
		t.Fatalf("explicit providers must be honoured: %v", h.cfg.Providers)
	}
}

func TestTerraEnvBoolAndIgnoreFlag(t *testing.T) {
	for v, want := range map[string]bool{"1": true, "true": true, "TRUE": true, " yes ": true, "on": true, "0": false, "": false, "no": false, "garmin": false} {
		t.Setenv("TERRA_IGNORE_GARMIN", v)
		if got := terraEnvBool("TERRA_IGNORE_GARMIN"); got != want {
			t.Errorf("terraEnvBool(%q) = %v, want %v", v, got, want)
		}
	}
	t.Setenv("TERRA_IGNORE_GARMIN", "")
	if NewTerraHandler(nil, TerraConfig{}, nil).cfg.IgnoreGarmin {
		t.Error("IgnoreGarmin must default to false")
	}
	t.Setenv("TERRA_IGNORE_GARMIN", "1")
	if !NewTerraHandler(nil, TerraConfig{}, nil).cfg.IgnoreGarmin {
		t.Error("TERRA_IGNORE_GARMIN=1 must enable IgnoreGarmin")
	}
	t.Setenv("TERRA_IGNORE_GARMIN", "")
	if !NewTerraHandler(nil, TerraConfig{IgnoreGarmin: true}, nil).cfg.IgnoreGarmin {
		t.Error("cfg.IgnoreGarmin must be honoured")
	}
}

func TestTerraIgnored_OnlyGarmin(t *testing.T) {
	h := NewTerraHandler(nil, TerraConfig{IgnoreGarmin: true}, nil)
	for src, want := range map[string]bool{"garmin": true, "coros": false, "polar": false, "suunto": false, "wahoo": false, "strava": false} {
		if got := h.terraIgnored(src); got != want {
			t.Errorf("terraIgnored(%q) = %v, want %v", src, got, want)
		}
	}
	if NewTerraHandler(nil, TerraConfig{}, nil).terraIgnored("garmin") {
		t.Error("garmin must not be ignored by default")
	}
}

func TestTerraIgnoreGarmin_ProvidersFilteredAndEmptyConnect503(t *testing.T) {
	h := NewTerraHandler(nil, TerraConfig{IgnoreGarmin: true, Providers: ParseTerraProviders("garmin,polar")}, nil)
	if strings.Join(h.cfg.Providers, ",") != "POLAR" {
		t.Fatalf("GARMIN must be removed from the widget providers: %v", h.cfg.Providers)
	}
	// 只列 GARMIN 又略過 → 空清單；Connect 必須 503（送空 providers 給 Terra 會變成顯示全部品牌）
	h = NewTerraHandler(nil, TerraConfig{IgnoreGarmin: true, Providers: ParseTerraProviders("garmin"), DevID: "d", APIKey: "k", SigningSecret: "s"}, nil)
	req := httptest.NewRequest(http.MethodGet, "/connect", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, "u"))
	rec := httptest.NewRecorder()
	h.Connect(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "terra_no_providers") {
		t.Fatalf("Connect with no providers: %d %s", rec.Code, rec.Body.String())
	}
}

// IgnoreGarmin：garmin 的 activity／auth 事件在碰 DB 之前就略過（repo=nil：若有任何 DB 存取會 panic）。
func TestTerraIgnoreGarmin_EventsSkippedWithoutTouchingDB(t *testing.T) {
	h := NewTerraHandler(nil, TerraConfig{IgnoreGarmin: true}, nil)
	ctx := context.Background()
	h.handleActivityEvent(ctx, []byte(`{"type":"activity","user":{"user_id":"t-1","provider":"GARMIN","reference_id":"00000000-0000-4000-8000-000000000009"},
		"data":[{"metadata":{"start_time":"2026-10-01T06:00:00Z","summary_id":"x","type":8},"distance_data":{"summary":{"distance_meters":5000}},"active_durations_data":{"activity_seconds":1800}}]}`))
	h.handleAuthEvent(ctx, []byte(`{"type":"auth","status":"success","user":{"user_id":"t-1","provider":"GARMIN","reference_id":"00000000-0000-4000-8000-000000000009"}}`))
}

func TestTerraIgnoreGarmin_ImportAndCallbackRefused(t *testing.T) {
	h := NewTerraHandler(nil, TerraConfig{IgnoreGarmin: true, DevID: "d", APIKey: "k", SigningSecret: "s", FrontendURL: "https://app.test/"}, nil)
	req := httptest.NewRequest(http.MethodPost, "/import?provider=garmin", nil)
	req = req.WithContext(context.WithValue(req.Context(), auth.CtxKeyUserID, "u"))
	rec := httptest.NewRecorder()
	h.Import(rec, req)
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "provider_unavailable") {
		t.Fatalf("Import(garmin): %d %s", rec.Code, rec.Body.String())
	}

	const uid, ref = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	h.hc = &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"user":{"user_id":"`+uid+`","provider":"GARMIN","reference_id":"`+ref+`"}}`), nil
	})}
	rec = httptest.NewRecorder()
	h.Callback(rec, httptest.NewRequest(http.MethodGet, "/callback?user_id="+uid+"&reference_id="+ref+"&resource=GARMIN", nil))
	loc := rec.Header().Get("Location")
	if rec.Code != http.StatusFound || !strings.Contains(loc, "terra=failed") || !strings.Contains(loc, "reason=provider_unavailable") {
		t.Fatalf("Callback(garmin) with IgnoreGarmin: %d %q", rec.Code, loc)
	}
}
