package race

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// chipIDRe 晶片號碼格式（D5）：選填，英數與連字號，至多 32 碼。晶片號碼是未來要串接第三方寵物資料
// 平台的識別碼，本輪只做寬鬆語法檢查，不像 ValidateInvoice 的手機條碼載具那樣需要正規化大小寫
// （沒有「同一組值兩種打法」的問題，該平台自己認定格式即可）。
var chipIDRe = regexp.MustCompile(`^[A-Za-z0-9-]{0,32}$`)

// ValidatePets 驗證並正規化報名 request 帶入的寵物名單（寵物雲端馬拉松，migration 173，D5）。
//
// race.PetKind==""（非寵物賽事）：直接略過，回傳 nil, nil——即使呼叫端多帶了 pets 也不擋，因為
// 前端這種賽事本來就不會顯示寵物 UI，防禦性地忽略比報錯更安全（避免未來欄位誤用鎖死既有報名流程）。
//
// race.PetKind!=""（寵物賽事）：
//   - 應有寵物數＝race.PetBaseSlots（目前固定 1，讀欄位不寫死）＋petSlotAddonQty（本次報名選購的
//     「加購寵物參賽名額」份數，由呼叫端(Service.Register)先從 req.Addons 對出 pet_slot 加購的 qty），
//     且不得超過 race.PetMaxPerReg（後台設定的每筆報名寵物上限）——這是前端 UI 已經 cap 過加購數量
//     選擇器的伺服器端最後防線。
//   - pets 一筆都沒有時回 ErrPetsRequired（比「數量不符」更精確的錯誤訊息，對應 D5「請填寫寵物資料」）。
//   - 其餘筆數不等於應有寵物數 → ErrPetCountMismatch。
//   - 每筆寵物：Name 去頭尾空白後需 1..40 個 rune，否則 ErrPetNameRequired；ChipID 選填，
//     需符合 chipIDRe，否則 ErrInvalidPetChip。
//
// 回傳正規化後的 []PetEntry（Name/ChipID 皆已 TrimSpace），供 Repository.RegisterWithOrder 寫入
// registration_pets（species 欄位由呼叫端用 race.PetKind 帶入，不存在 PetEntry 上）。
func ValidatePets(race *Race, pets []PetEntry, petSlotAddonQty int) ([]PetEntry, error) {
	if race == nil || race.PetKind == "" {
		return nil, nil
	}

	want := race.PetBaseSlots + petSlotAddonQty
	if want <= 0 {
		want = 1
	}
	if want > race.PetMaxPerReg {
		return nil, ErrPetCountMismatch
	}
	if len(pets) == 0 {
		return nil, ErrPetsRequired
	}
	if len(pets) != want {
		return nil, ErrPetCountMismatch
	}

	out := make([]PetEntry, len(pets))
	for i, p := range pets {
		name := strings.TrimSpace(p.Name)
		if name == "" || utf8.RuneCountInString(name) > 40 {
			return nil, ErrPetNameRequired
		}
		chip := strings.TrimSpace(p.ChipID)
		if !chipIDRe.MatchString(chip) {
			return nil, ErrInvalidPetChip
		}
		out[i] = PetEntry{Name: name, ChipID: chip}
	}
	return out, nil
}
