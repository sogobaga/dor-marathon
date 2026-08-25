package virtualrunner

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// cityCoord 城市座標（緯度/經度），供 Open-Meteo /v1/forecast 查詢用。七都座標寫死
// （生成規格②定案；不查資料庫/不做地理編碼——這批城市固定不變，且與 model.go cityList
// 同一份白名單，見 weather_test.go TestCityCoords_CoversAllSevenCities 交叉驗證兩邊不會漂移）。
type cityCoord struct{ lat, lng float64 }

var cityCoords = map[string]cityCoord{
	"taipei":     {25.0330, 121.5654},
	"new_taipei": {25.0120, 121.4657},
	"taoyuan":    {24.9936, 121.3010},
	"hsinchu":    {24.8138, 120.9675},
	"taichung":   {24.1477, 120.6736},
	"tainan":     {22.9999, 120.2270},
	"kaohsiung":  {22.6273, 120.3014},
}

// weatherBadTempC / weatherBadPrecipMM 壞天氣判定門檻（生成規格②定案：降雨>0.5mm 或 氣溫>=32°C，
// 任一成立即算——酷熱與下雨都會讓人想偷懶）。
const (
	weatherBadTempC    = 32.0
	weatherBadPrecipMM = 0.5
)

// Weather 單一城市當下天氣（Open-Meteo current 區塊，只取生成引擎用得到的兩個欄位）。
type Weather struct {
	TemperatureC    float64
	PrecipitationMM float64
}

// IsBad 是否為「壞天氣」，見生成規格②。
func (w Weather) IsBad() bool {
	return w.PrecipitationMM > weatherBadPrecipMM || w.TemperatureC >= weatherBadTempC
}

type openMeteoResp struct {
	Current struct {
		Temperature2m float64 `json:"temperature_2m"`
		Precipitation float64 `json:"precipitation"`
	} `json:"current"`
}

// WeatherClient Open-Meteo 天氣查詢（免金鑰、免額度限制的公開 API）。
type WeatherClient struct {
	hc *http.Client
}

// NewWeatherClient timeout 10s；寫法比照 internal/integration/strava.go getJSON 範本
// （該處 Strava API 用 15s，考量回應體較大/第三方較不穩定——Open-Meteo /forecast 回應僅兩個數字，
// 10s 已足夠寬裕）。
func NewWeatherClient() *WeatherClient {
	return &WeatherClient{hc: &http.Client{Timeout: 10 * time.Second}}
}

// fetch 查詢單一城市當下天氣；city 不在白名單（cityCoords）或 HTTP/JSON 失敗皆回傳 error，
// fallback 政策由呼叫端（WeatherCache.Get）決定——生成規格②「API 失敗→視為正常天氣、log warn，
// 不告警」：天氣只是生成引擎的機率調味料，不是金流等關鍵路徑，沒必要為此驚動 Telegram。
func (c *WeatherClient) fetch(ctx context.Context, city string) (Weather, error) {
	coord, ok := cityCoords[city]
	if !ok {
		return Weather{}, fmt.Errorf("virtualrunner: unknown city %q for weather lookup", city)
	}
	url := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f&current=temperature_2m,precipitation",
		coord.lat, coord.lng)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Weather{}, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return Weather{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Weather{}, fmt.Errorf("open-meteo http %d", resp.StatusCode)
	}
	var out openMeteoResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Weather{}, err
	}
	return Weather{TemperatureC: out.Current.Temperature2m, PrecipitationMM: out.Current.Precipitation}, nil
}

// weatherFetchFunc 由 WeatherClient.fetch 或測試替身注入，供 WeatherCache 呼叫——用函式型別
// 而非介面，測試不需另建假物件即可替換（見 weather_test.go）。
type weatherFetchFunc func(ctx context.Context, city string) (Weather, error)

// WeatherCache 單一生成批次內的天氣快取：每座城市在同一批次最多真的打一次 Open-Meteo
// （生成規格②「每批次每城市查一次」）；其餘同城市選手直接讀快取。查詢失敗的城市也會快取
// fallback「正常天氣」結果，避免同一批次內對同一個失敗中的城市重複重試/重複打 API。
type WeatherCache struct {
	fetch weatherFetchFunc
	cache map[string]Weather
}

// NewWeatherCache 供 Generator 正式執行使用。
func NewWeatherCache(client *WeatherClient) *WeatherCache {
	return &WeatherCache{fetch: client.fetch, cache: make(map[string]Weather)}
}

// newWeatherCacheWithFetch 測試用建構子：注入假的 fetch 函式，不打真正的網路請求。
func newWeatherCacheWithFetch(fetch weatherFetchFunc) *WeatherCache {
	return &WeatherCache{fetch: fetch, cache: make(map[string]Weather)}
}

// Get 該城市這個批次的天氣（快取命中直接回傳；未命中才真的查詢）。查詢失敗回傳「正常天氣」
// 並 log warn，不回錯誤——呼叫端（Generator.runBatch）不應該因單一城市的天氣查詢失敗而整批中止
// 或誤判為異常需要告警。
func (c *WeatherCache) Get(ctx context.Context, city string) Weather {
	if w, ok := c.cache[city]; ok {
		return w
	}
	w, err := c.fetch(ctx, city)
	if err != nil {
		log.Warn().Err(err).Str("city", city).
			Msg("virtual runner generator: open-meteo fetch failed, treating as normal weather")
		w = Weather{}
	}
	c.cache[city] = w
	return w
}
