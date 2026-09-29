package gpsrawlog

import "testing"

func validRow() []any {
	return []any{float64(1000), 25.033, 121.5645, 8.5, 2.7, 90.0, "a"}
}

func TestValidateRow_Valid(t *testing.T) {
	if err := validateRow(validRow()); err != nil {
		t.Fatalf("expected valid row to pass, got %v", err)
	}
}

func TestValidateRow_NullableSpeedHeading(t *testing.T) {
	row := validRow()
	row[4] = nil
	row[5] = nil
	if err := validateRow(row); err != nil {
		t.Fatalf("expected nil speed/heading to be allowed, got %v", err)
	}
}

func TestValidateRow_WrongLength(t *testing.T) {
	row := []any{float64(1000), 25.033, 121.5645}
	if err := validateRow(row); err == nil {
		t.Fatal("expected error for wrong row length")
	}
}

func TestValidateRow_LatOutOfRange(t *testing.T) {
	row := validRow()
	row[1] = 91.0
	if err := validateRow(row); err == nil {
		t.Fatal("expected error for lat out of range")
	}
	row[1] = -91.0
	if err := validateRow(row); err == nil {
		t.Fatal("expected error for lat out of range (negative)")
	}
}

func TestValidateRow_LngOutOfRange(t *testing.T) {
	row := validRow()
	row[2] = 181.0
	if err := validateRow(row); err == nil {
		t.Fatal("expected error for lng out of range")
	}
}

func TestValidateRow_NegativeAcc(t *testing.T) {
	row := validRow()
	row[3] = -1.0
	if err := validateRow(row); err == nil {
		t.Fatal("expected error for negative acc")
	}
}

func TestValidateRow_BadSpeedType(t *testing.T) {
	row := validRow()
	row[4] = "fast"
	if err := validateRow(row); err == nil {
		t.Fatal("expected error for non-numeric non-nil speed")
	}
}

func TestValidateRow_BadHeadingType(t *testing.T) {
	row := validRow()
	row[5] = "north"
	if err := validateRow(row); err == nil {
		t.Fatal("expected error for non-numeric non-nil heading")
	}
}

func TestValidateRow_InvalidCode(t *testing.T) {
	row := validRow()
	row[6] = "z"
	if err := validateRow(row); err == nil {
		t.Fatal("expected error for unknown code")
	}
}

func TestValidateRow_QuestionMarkCodeAllowed(t *testing.T) {
	row := validRow()
	row[6] = "?"
	if err := validateRow(row); err != nil {
		t.Fatalf("expected '?' code to be allowed, got %v", err)
	}
}

func TestValidateRow_AllKnownCodes(t *testing.T) {
	for code := range validCodes {
		row := validRow()
		row[6] = code
		if err := validateRow(row); err != nil {
			t.Errorf("code %q should be valid, got %v", code, err)
		}
	}
}

func TestUploadPayloadValidate_OK(t *testing.T) {
	p := UploadPayload{V: 1, Rows: [][]any{validRow(), validRow()}}
	if err := p.Validate(); err != nil {
		t.Fatalf("expected valid payload, got %v", err)
	}
}

func TestUploadPayloadValidate_EmptyRowsOK(t *testing.T) {
	p := UploadPayload{V: 1, Rows: nil}
	if err := p.Validate(); err != nil {
		t.Fatalf("expected empty rows to be valid, got %v", err)
	}
}

func TestUploadPayloadValidate_WrongVersion(t *testing.T) {
	p := UploadPayload{V: 2, Rows: [][]any{validRow()}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for unsupported version")
	}
}

func TestUploadPayloadValidate_ZeroVersionRejected(t *testing.T) {
	// 缺 "v" 欄位（JSON 解碼後為零值 0）也應視為不支援版本，而非默默當成 v1。
	p := UploadPayload{Rows: [][]any{validRow()}}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for missing/zero version")
	}
}

func TestUploadPayloadValidate_TooManyRows(t *testing.T) {
	rows := make([][]any, maxRows+1)
	for i := range rows {
		rows[i] = validRow()
	}
	p := UploadPayload{V: 1, Rows: rows}
	if err := p.Validate(); err == nil {
		t.Fatal("expected error for exceeding maxRows")
	}
}

func TestUploadPayloadValidate_ExactlyMaxRowsOK(t *testing.T) {
	rows := make([][]any, maxRows)
	for i := range rows {
		rows[i] = validRow()
	}
	p := UploadPayload{V: 1, Rows: rows}
	if err := p.Validate(); err != nil {
		t.Fatalf("expected maxRows itself to be valid, got %v", err)
	}
}

func TestUploadPayloadValidate_PropagatesRowIndex(t *testing.T) {
	rows := [][]any{validRow(), validRow()}
	rows[1][6] = "bad-code"
	p := UploadPayload{V: 1, Rows: rows}
	err := p.Validate()
	if err == nil {
		t.Fatal("expected error")
	}
	// 錯誤訊息應點名是第幾列（0-based 索引 1），方便除錯定位壞資料。
	if got := err.Error(); got == "" {
		t.Fatal("expected non-empty error message")
	}
}
