package race

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// 進度頁每日歷程（本人視圖）的 JSON 契約：device_name 只在有型號時輸出（NULL 不輸出），source 一律輸出；
// 前台依這兩個欄位顯示「Garmin 〈型號〉」歸屬（型號不明時只顯示品牌）。
func TestDailyActivityJSONCarriesSourceAndOptionalDeviceName(t *testing.T) {
	model := "Forerunner 265"
	with, err := json.Marshal(DailyActivity{RecordedAt: time.Unix(1790000000, 0).UTC(), DistanceKm: 5, Source: "garmin", ExternalID: "gc:1", DeviceName: &model})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(with), `"source":"garmin"`) || !strings.Contains(string(with), `"device_name":"Forerunner 265"`) {
		t.Fatalf("garmin row must carry source and device_name: %s", with)
	}
	without, err := json.Marshal(DailyActivity{RecordedAt: time.Unix(1790000000, 0).UTC(), DistanceKm: 5, Source: "garmin", ExternalID: "gc:2"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(without), "device_name") || !strings.Contains(string(without), `"source":"garmin"`) {
		t.Fatalf("unknown model: device_name must be omitted, source kept: %s", without)
	}
}
