package einvoice

import (
	"encoding/json"
	"testing"
)

// TestRespEnvelope_NumericMerchantID 正式環境實測：綠界回應外層 MerchantID 為 JSON 數字、TransCode 可能為字串。
func TestRespEnvelope_NumericMerchantID(t *testing.T) {
	raw := []byte(`{"MerchantID":3504994,"RpHeader":{"Timestamp":1757150000},"TransCode":"1","TransMsg":"","Data":"abc"}`)
	var env respEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if string(env.MerchantID) != "3504994" || int(env.TransCode) != 1 || int(env.RpHeader.Timestamp) != 1757150000 || env.Data != "abc" {
		t.Fatalf("unexpected envelope: %+v", env)
	}
	var env2 respEnvelope
	if err := json.Unmarshal([]byte(`{"MerchantID":"2000132","RpHeader":{"Timestamp":1},"TransCode":1,"TransMsg":"x","Data":""}`), &env2); err != nil {
		t.Fatalf("string form: %v", err)
	}
	if string(env2.MerchantID) != "2000132" || int(env2.TransCode) != 1 {
		t.Fatalf("unexpected envelope2: %+v", env2)
	}
}
