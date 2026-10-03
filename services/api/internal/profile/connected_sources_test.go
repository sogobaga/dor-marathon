package profile

// Dashboard 的 connected_sources／garmin_connected 推導（S8 後端部分，membership.go）。

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestDeriveConnectedSources(t *testing.T) {
	cases := []struct {
		name         string
		rows         []integrationConnRow
		wantSources  []string
		wantCorosMcp bool
		wantGarminD  bool
	}{
		{"no connections → empty (non-nil) list", nil, []string{}, false, false},
		{"strava only", []integrationConnRow{{"strava", "direct"}}, []string{"strava"}, false, false},
		{"coros MCP counts as coros and reports corosMcp", []integrationConnRow{{"coros_mcp", "direct"}}, []string{"coros"}, true, false},
		{"coros via Terra", []integrationConnRow{{"coros", "terra"}}, []string{"coros"}, false, false},
		{"coros terra + coros mcp → one coros", []integrationConnRow{{"coros", "terra"}, {"coros_mcp", "direct"}}, []string{"coros"}, true, false},
		{"garmin direct", []integrationConnRow{{"garmin", "direct"}}, []string{"garmin"}, false, true},
		{"garmin via terra is a source but not 'direct'", []integrationConnRow{{"garmin", "terra"}}, []string{"garmin"}, false, false},
		{"fixed canonical order regardless of row order",
			[]integrationConnRow{{"wahoo", "terra"}, {"coros_mcp", "direct"}, {"strava", "direct"}, {"garmin", "direct"}, {"polar", "terra"}, {"suunto", "terra"}},
			[]string{"strava", "garmin", "coros", "polar", "suunto", "wahoo"}, true, true},
		{"unknown providers ignored", []integrationConnRow{{"fitbit", "direct"}, {"strava", "direct"}}, []string{"strava"}, false, false},
		{"duplicates collapse", []integrationConnRow{{"strava", "direct"}, {"strava", "direct"}}, []string{"strava"}, false, false},
	}
	for _, c := range cases {
		got, corosMcp, garminDirect := deriveConnectedSources(c.rows)
		if !reflect.DeepEqual(got, c.wantSources) || corosMcp != c.wantCorosMcp || garminDirect != c.wantGarminD {
			t.Errorf("%s: got (%v,%v,%v) want (%v,%v,%v)", c.name, got, corosMcp, garminDirect, c.wantSources, c.wantCorosMcp, c.wantGarminD)
		}
	}
}

// JSON 形狀：connected_sources 恆為陣列（不是 null）、garmin_entry／garmin_connected／coros_mcp_entry 存在。
func TestDashboardInfoJSONShapeForDirectWearables(t *testing.T) {
	srcs, _, _ := deriveConnectedSources(nil)
	b, err := json.Marshal(DashboardInfo{ConnectedSources: srcs, CorosMcpEntry: "shown", GarminEntry: "hidden"})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	if v, ok := m["connected_sources"].([]any); !ok || len(v) != 0 {
		t.Fatalf("connected_sources must be [] not null: %v", m["connected_sources"])
	}
	for _, k := range []string{"coros_mcp_entry", "garmin_entry", "garmin_connected"} {
		if _, ok := m[k]; !ok {
			t.Errorf("dashboard JSON missing %q", k)
		}
	}
	if m["coros_mcp_entry"] != "shown" || m["garmin_entry"] != "hidden" || m["garmin_connected"] != false {
		t.Errorf("unexpected values: %v", m)
	}
}
