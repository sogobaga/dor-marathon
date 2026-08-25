package virtualrunner

import (
	"context"
	"errors"
	"testing"
)

// TestWeather_IsBad 壞天氣判定門檻：降雨>0.5mm 或 氣溫>=32°C，任一成立即算。
func TestWeather_IsBad(t *testing.T) {
	cases := []struct {
		name string
		w    Weather
		want bool
	}{
		{"晴天涼爽", Weather{TemperatureC: 25, PrecipitationMM: 0}, false},
		{"降雨剛好門檻不算(嚴格大於)", Weather{TemperatureC: 25, PrecipitationMM: 0.5}, false},
		{"降雨超過門檻", Weather{TemperatureC: 25, PrecipitationMM: 0.6}, true},
		{"高溫剛好門檻(>=)", Weather{TemperatureC: 32, PrecipitationMM: 0}, true},
		{"高溫略低於門檻", Weather{TemperatureC: 31.9, PrecipitationMM: 0}, false},
		{"又雨又熱", Weather{TemperatureC: 33, PrecipitationMM: 1}, true},
	}
	for _, c := range cases {
		if got := c.w.IsBad(); got != c.want {
			t.Errorf("%s: IsBad() = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestWeatherCache_CachesPerCity 同一批次內同一城市只會真的查詢一次。
func TestWeatherCache_CachesPerCity(t *testing.T) {
	calls := map[string]int{}
	fetch := func(ctx context.Context, city string) (Weather, error) {
		calls[city]++
		return Weather{TemperatureC: 20, PrecipitationMM: 0}, nil
	}
	cache := newWeatherCacheWithFetch(fetch)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		cache.Get(ctx, "taipei")
	}
	cache.Get(ctx, "kaohsiung")
	if calls["taipei"] != 1 {
		t.Fatalf("taipei 應只真的查詢一次，實際 %d 次", calls["taipei"])
	}
	if calls["kaohsiung"] != 1 {
		t.Fatalf("kaohsiung 應只真的查詢一次，實際 %d 次", calls["kaohsiung"])
	}
}

// TestWeatherCache_FetchFailureFallsBackToNormalWeather API 失敗時 fallback 為「正常天氣」
// （非壞天氣），且失敗結果也會被快取（同批次不重複重試同一個失敗中的城市）。
func TestWeatherCache_FetchFailureFallsBackToNormalWeather(t *testing.T) {
	calls := 0
	fetch := func(ctx context.Context, city string) (Weather, error) {
		calls++
		return Weather{}, errors.New("open-meteo unreachable")
	}
	cache := newWeatherCacheWithFetch(fetch)
	ctx := context.Background()

	w := cache.Get(ctx, "taichung")
	if w.IsBad() {
		t.Fatal("查詢失敗應 fallback 為正常天氣（非壞天氣）")
	}

	cache.Get(ctx, "taichung")
	if calls != 1 {
		t.Fatalf("失敗結果也應快取，避免同批次對同一城市重複查詢；實際查詢 %d 次", calls)
	}
}

// TestWeatherClient_FetchUnknownCity 城市不在白名單時直接回 error（不會打出無意義的 HTTP 請求）。
func TestWeatherClient_FetchUnknownCity(t *testing.T) {
	c := NewWeatherClient()
	if _, err := c.fetch(context.Background(), "keelung"); err == nil {
		t.Fatal("未知城市應回傳 error")
	}
}

// TestCityCoords_CoversAllSevenCities cityCoords（weather.go）與 cityList（model.go）必須是
// 同一份七都白名單，不會兩邊各自維護到漂移。
func TestCityCoords_CoversAllSevenCities(t *testing.T) {
	for _, city := range cityList {
		if _, ok := cityCoords[city]; !ok {
			t.Errorf("cityCoords 缺少城市 %q 的座標", city)
		}
	}
	if len(cityCoords) != len(cityList) {
		t.Errorf("cityCoords 有 %d 筆，cityList 有 %d 筆，數量應一致", len(cityCoords), len(cityList))
	}
}
