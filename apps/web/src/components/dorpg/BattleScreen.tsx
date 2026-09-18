'use client';

// DORPG P1 戰鬥畫面組裝層：把 TopBar／PartyCard×5／BattleStage／TargetBar／SkillTray／CommandBar
// 依規格書 §3 表 3 的五個固定帶高垂直疊起來，場景吃剩餘高度（版面規則沿用 P0，未改動）。
// P1 起，戰鬥規則／AI／計時全部交給 @/lib/dorpg/useBattle 包住的純函式引擎，這個檔案只負責：
//   1) 把引擎事件（BattleEvent）翻譯成畫面效果（特效／音效／飄字／震動）；
//   2) 把使用者手勢（按鈕/長按/點卡片/點怪物）翻譯成引擎指令（Command）；
//   3) 把引擎狀態投影成各子元件要的 props 形狀（PartyActor→PartyMember、EnemyActor→StageEnemy 等）。
// 效能：useBattle 的 state 只在引擎真的有新事件（seq 前進）時才變（見 useBattle.ts），但攻擊蓄氣條／
// 冷卻秒數需要逐幀讀數——這裡另外用兩個獨立的 rAF effect 讀 battle.liveStateRef（不觸發引擎重算，
// 只是讀「這一刻最新」的數字），且只在顯示值真的變了才 setState。為了不讓這兩個高頻 setState
// 拖著 PartyCard×5／BattleStage（內含 5 個 MonsterSprite canvas＋CombatFxLayer）一起重繪，
// 這幾個子元件在檔尾用 React.memo 包一層：只要傳給它們的 props 沒變，記憶體裡的舊渲染結果就直接沿用。
import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import type { BattleSample, BtnState, BuffDebuffStat, EscapeState, Item, PartyMember, TrayMode } from '@/lib/dorpg/types';
import { ENEMY_PLATE, KIT_SIZES, LAYOUT, PALETTE, SCENE_VIEWPORT, kitAsset, layoutFor, nineSliceStyle, sceneHeightFor } from '@/lib/dorpg/assets';
import { skillStatLabel } from '@/lib/rpgMeta';
import { SAMPLE_BATTLE } from '@/lib/dorpg/sampleBattle';
import TopBar from './TopBar';
import PartyCard from './PartyCard';
import BattleStage, { type BattleStageHandle, type StageEnemy } from './BattleStage';
import TargetBar from './TargetBar';
import SkillTray from './SkillTray';
import CommandBar from './CommandBar';
import ResultOverlay from './ResultOverlay';
import FloatText, { type FloatTextTone } from './FloatText';
import enemyPlateStyles from './EnemyPlate.module.css';
import styles from './BattleScreen.module.css';
import { useBattle } from '@/lib/dorpg/useBattle';
import { chargeRatio as engineChargeRatio } from '@/lib/dorpg/engine';
import type { BattleConfig, BattleEvent, BattleState, PartyActor } from '@/lib/dorpg/engine';
import { useHoldGesture } from '@/lib/dorpg/useHoldGesture';
import { battleAudio } from '@/lib/dorpg/audio';

/**
 * P2：戰鬥結束時交給上層（PhoneShell／dev Preview）打 POST /rpg/battle/report 的純統計（契約 §4）。
 * 只含 BattleScreen 自己量得到的數字——encounter_code 由上層從呼叫端已知的 code 補上，
 * player_level/player_power/client_version 由上層依 bootstrap 的角色資料/APP_VERSION 補上，
 * 這樣 BattleScreen 不需要認識「玩家角色」以外的任何後端概念。
 */
export type BattleReportStats = {
  encounterCode: string;
  // 2026-09-14 P2 修正第1輪 審查6：新增 'abandoned'（中途離開，尚未分出勝負）——後端
  // rpg_battle_logs.outcome 與後台 outcomeLabel() 本來就已預留這個值，只差前端從未有路徑產生過。
  outcome: 'victory' | 'defeat' | 'draw' | 'escaped' | 'abandoned';
  durationMs: number;
  damageDealt: number;
  damageTaken: number;
  enemiesDefeated: number;
  attacks: number;
  chargedAttacks: number;
  skillsUsed: number;
  itemsUsed: number;
  guardMs: number;
};

/** 契約 §4：BattleScreen 只需要知道遭遇的 code（組 report）；title 純供未來擴充顯示用，非必要。 */
export type BattleScreenEncounter = { code: string; title?: string };

const DEFAULT_ENCOUNTER: BattleScreenEncounter = { code: 'sample' };

export type BattleScreenProps = {
  onBack: () => void;
  /** 未給時用 SAMPLE_BATTLE（kit 測試資料＋content pack 素材）。 */
  sample?: BattleSample;
  /** P2：後端 bootstrap 回應的 config（見 fromApi.ts configFromBootstrap）；未給則全部用引擎預設值。 */
  config?: Partial<BattleConfig>;
  /** P2：目前這場遭遇（供組 report 用）；未給時退回 DEFAULT_ENCOUNTER（/dev 預覽等尚未接真實遭遇的呼叫端）。 */
  encounter?: BattleScreenEncounter;
  /** P2：「再戰一場」（同一遭遇重來）；未給則 ResultOverlay 不顯示這顆鈕。 */
  onRestart?: () => void;
  /** P2：「換一場」（回遭遇選單）；未給則 ResultOverlay 不顯示這顆鈕。 */
  onNext?: () => void;
  /** P2：戰鬥結束時回報統計，上層打 API、失敗只 console.warn（契約 §4，不擋 UI）。 */
  onReport?: (stats: BattleReportStats) => void;
  /**
   * 是否允許 `?dorpgDebug=1` 生效（預設 false）。2026-09-14 審查抓到：原本只憑 URL query 判斷，
   * 正式站只要在網址加這個參數就能把敵人血量全部壓到 1、還會掛 window.__dorpgBattle 讓任何人直接
   * 用 console 操控戰鬥——只有 app/dev/dorpg/Preview.tsx（DORPG_DEV=1 才存在的開發預覽頁）會傳
   * true，其餘呼叫端（正式戰鬥入口）不傳就等於永遠關閉，query 本身不再具備任何效力。
   */
  debugAllowed?: boolean;
};

/** 隊伍列：左右留 6、卡距 4、五張（規格表 7）。 */
const PARTY_PAD_X = 6;
const PARTY_GAP = 4;
const PARTY_COUNT = 5;
/** 目標列寬 = W − 12（規格表 11、kit preview #target-row）。 */
const TARGET_INSET = 12;
/** 場景最小高：再矮就讓整欄捲動，不把怪物壓扁。 */
const MIN_STAGE_H = 120;
/** 規格：H<520 或 W<360 保留最小容器並可捲動。 */
const SCROLL_MIN_H = 520;
const SCROLL_MIN_W = 360;
/** icon_close 邏輯 44×44（manifest；KIT_SIZES 未列，與 icon_settings 同尺寸）。 */
const ICON_CLOSE = 44;

/**
 * P5 POLISH：EnemyPlate（怪物 Lv/HP 面板）在 BattleStage.tsx 內部用同名常數把面板頂邊定在
 * 「腳點 − 5 邏輯 px」（見該檔 PLATE_LIFT 註解）。EnemyPlate 本體與怪物站位公式都定義在
 * BattleStage.tsx（本輪 POLISH-UI 寫入範圍不含該檔），這裡的敵人 buff/debuff 標籤列改在
 * BattleScreen.tsx 自己重算一次站位、疊一層獨立 overlay（見 enemyTagOverlay），數字必須跟
 * BattleStage.tsx 那份保持一致，才能讓標籤剛好貼在面板正上方——重複一份純位置常數是本輪
 * 寫入範圍限制下的必要代價，不是選擇上的疏忽。
 */
const PLATE_LIFT_DUP = 5;

/** 開場預載的 8 個音效 id：SAMPLE_BATTLE 唯一用到的兩種武器（sword／staff）× 四種結果。
 *  之後若樣本資料加入 bow/greatsword，這份清單要跟著補，見 fxManifest.ts 的 FX_MANIFEST.audio。 */
const PRELOAD_SFX_IDS = [
  'sfx_sword_normal', 'sfx_sword_critical', 'sfx_sword_miss', 'sfx_sword_immune',
  'sfx_staff_normal', 'sfx_staff_critical', 'sfx_staff_miss', 'sfx_staff_immune',
];

const LS_VIBE = 'dorpg.vibe';
function readVibeSetting(): boolean {
  if (typeof window === 'undefined') return true;
  try {
    const raw = window.localStorage.getItem(LS_VIBE);
    return raw === null ? true : raw === 'true';
  } catch {
    return true; // 私密瀏覽模式等情況下 localStorage 可能丟例外，退回預設值（震動預設開）
  }
}
function writeVibeSetting(v: boolean) {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(LS_VIBE, String(v));
  } catch {
    /* 略過：不影響本次設定，只是下次不會記得 */
  }
}

/** PartyActor（引擎戰鬥中資料）→ PartyMember（PartyCard 吃的展示形狀）。兩邊欄位語意相同、
 *  只是 hpMax/mpMax 在引擎那邊收在 stats 裡——跟 MonsterSprite 的 adaptAnimSet 是同一種轉接手法。
 *  DORPG P6（契約 §1：HP/MP 務必整數）：Math.floor 是顯示層保險，PartyCard.tsx 內部也會再 floor
 *  一次——兩處都保是刻意的，任一層以後被改動都不會讓小數點重新露出來。 */
function toPartyMemberView(actor: PartyActor): PartyMember {
  return {
    id: actor.id,
    name: actor.name,
    level: actor.level,
    hp: Math.floor(actor.hp),
    hpMax: Math.floor(actor.stats.hpMax),
    mp: Math.floor(actor.mp),
    mpMax: Math.floor(actor.stats.mpMax),
    portraitUrl: actor.portraitUrl,
  };
}

type SettingsValues = { music: number; sfx: number; vibrate: boolean; reduceMotion: boolean };

/**
 * 累積整場戰鬥的統計數字（跨事件累加，不屬於引擎狀態——引擎只在乎「現在」，摘要是 UI 自己記的）。
 * P2 新增給 onReport 用的欄位：attacks/chargedAttacks 只算「玩家本人」的普攻（engine 的 'attack' 事件
 * 普攻/技能傷害共用同一個 kind，不分是誰觸發——見下面 handleBattleEvents 的 pendingSkillDamageRef
 * 說明）；damageTaken 是全隊（含隊友）受到的傷害合計，跟既有 damageDealt 的口徑（全隊造成）對稱。
 */
type BattleStats = {
  damageDealt: number;
  damageTaken: number;
  defeatedLevels: number[];
  attacks: number;
  chargedAttacks: number;
  skillsUsed: number;
  itemsUsed: number;
  guardMs: number;
};

// 高頻更新（蓄氣條／冷卻倒數）只會改變 CommandBar/SkillTray 的 props，其餘子樹用 memo 包起來，
// 讓它們在那些 tick 期間直接沿用舊渲染結果，不必每幀重新計算整棵 PartyCard×5／BattleStage。
const MemoPartyCard = memo(PartyCard);
const MemoBattleStage = memo(BattleStage);
const MemoTargetBar = memo(TargetBar);
const MemoTopBar = memo(TopBar);

export default function BattleScreen({
  onBack,
  sample: sampleProp = SAMPLE_BATTLE,
  config,
  encounter = DEFAULT_ENCOUNTER,
  onRestart,
  onNext,
  onReport,
  debugAllowed = false,
}: BattleScreenProps) {
  // ---- 除錯模式（契約 §7/§8）----
  // ?dorpgDebug=1 時把敵人血量全部壓到 1：verify 腳本才能用「真的按住攻擊鈕」這種寫實輸入，在合理秒數內
  // 打完五隻全滅→勝利面板的完整流程，而不必另外開一條直接改引擎內部狀態的後門（那樣就驗證不到真實手感）。
  // 2026-09-14 審查修正：一定要先過 debugAllowed（只有開發預覽頁會傳 true）才看 query，否則正式站
  // 隨便加個 ?dorpgDebug=1 就能把敵人血量清空、還能掛 window.__dorpgBattle 操控戰鬥。
  const [dorpgDebug] = useState(
    () => debugAllowed && typeof window !== 'undefined' && new URLSearchParams(window.location.search).get('dorpgDebug') === '1',
  );
  const sample = useMemo(() => {
    if (!dorpgDebug) return sampleProp;
    return { ...sampleProp, enemies: sampleProp.enemies.map((e) => ({ ...e, hp: 1, hpMax: 1 })) };
  }, [dorpgDebug, sampleProp]);

  // ---- 量測自身尺寸（clientWidth/Height 是版面 px，不受桌機 .phone-shell 的 transform 影響） ----
  const rootRef = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState<{ w: number; h: number }>({ w: 0, h: 0 });
  useLayoutEffect(() => {
    const el = rootRef.current;
    if (!el) return;
    const measure = () => {
      const w = Math.min(LAYOUT.maxWidth, Math.round(el.clientWidth));
      const h = Math.round(el.clientHeight);
      setSize((prev) => (prev.w === w && prev.h === h ? prev : { w, h }));
    };
    measure();
    if (typeof ResizeObserver === 'undefined') {
      window.addEventListener('resize', measure);
      return () => window.removeEventListener('resize', measure);
    }
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // ---- 本地 UI 狀態（不屬於戰鬥規則的部分：瀏覽用選取、托盤翻頁、設定面板） ----
  const [memberIdx, setMemberIdx] = useState(0);
  const [trayOffset, setTrayOffset] = useState(0);
  const [showSettings, setShowSettings] = useState(false);
  const [settings, setSettings] = useState<SettingsValues>(() => {
    const v = battleAudio.getVolumes();
    return { music: v.music, sfx: v.sfx, vibrate: readVibeSetting(), reduceMotion: false };
  });
  const settingsRef = useRef(settings);
  settingsRef.current = settings;

  const [floatTexts, setFloatTexts] = useState<Record<string, { text: string; tone: FloatTextTone; key: number }>>({});
  const floatKeyRef = useRef(0);
  const pushFloat = useCallback((memberId: string, text: string, tone: FloatTextTone) => {
    floatKeyRef.current += 1;
    const key = floatKeyRef.current;
    setFloatTexts((prev) => ({ ...prev, [memberId]: { text, tone, key } }));
  }, []);

  const stageRef = useRef<BattleStageHandle>(null);

  // P5 POLISH：敵人身上的 statusApplied 飄字（buff 打隊友走既有 floatTexts／PartyCard，debuff 打
  // 敵人沒有現成的容器可掛百分比座標）——改記一份「目前戰場上飄字」映射，位置用 BattleStageHandle
  // 已對外開放的 getEnemyAnchor() 現查（跟下面 'attack' 分支算特效座標用的是同一支函式），交給
  // FloatText 的「定點模式」（見 FloatText.tsx anchorPx）渲染。跟 floatTexts／pushFloat 同一種
  // 「key 換新值即重播」設計，找不到站位（例如敵人剛好死亡動畫播完被移除）就略過，不強求一定要飄出來。
  const [enemyFloatTexts, setEnemyFloatTexts] = useState<Record<string, { text: string; tone: FloatTextTone; key: number; x: number; y: number; small?: boolean }>>({});
  const enemyFloatKeyRef = useRef(0);
  // DORPG P7：新增選填的 `small` 參數，供斧的濺射傷害用較小樣式（見 FloatText.tsx small prop）；
  // 既有呼叫端（statusApplied 的 buff/debuff）不傳這個參數，行為完全不變。
  // 審查#5【低・UI】根因修復：getEnemyAnchor 讀的是 BattleStage 內部 geomRef——那份快照鎖定在
  // BattleStage「上一次 render commit」當下的 props，而這裡是在 useBattle.ts commitEvents 裡
  // 「setState 之前」同步呼叫（見該檔案頭註解），兩者之間天生有一個 race window：畫面剛掛載、
  // ResizeObserver 還沒量到真正尺寸，或短時間內連續好幾批事件都搶在同一次 render commit 之前發生時，
  // geomRef 可能跟不上最新狀態，讓 getEnemyAnchor 對明明還在場上的敵人也查無站位、回 null——原本
  // 「查無錨點就靜默不顯示」的規則會讓這下濺射完全沒有畫面回饋（傷害其實有算、有扣血）。新增選填
  // `fallbackAnchor`：查不到自己的錨點時退而求其次改用它（見下方 case 'attack' 的呼叫端——濺射一定
  // 緊跟在同一次攻擊的主擊事件之後，主擊的錨點在同一輪 handleBattleEvents 內一定已經先算出來），
  // 讓玩家至少看得到數字飄在主目標旁邊，不會整個消失；兩者都拿不到才真的放棄（維持原有行為）。
  const pushEnemyFloat = useCallback((enemyId: string, text: string, tone: FloatTextTone, small?: boolean, fallbackAnchor?: { x: number; y: number }) => {
    const anchor = stageRef.current?.getEnemyAnchor(enemyId) ?? fallbackAnchor ?? null;
    if (!anchor) return;
    enemyFloatKeyRef.current += 1;
    const key = enemyFloatKeyRef.current;
    setEnemyFloatTexts((prev) => ({ ...prev, [enemyId]: { text, tone, key, x: anchor.x, y: anchor.y, small } }));
  }, []);

  // 審查#5：濺射目標永遠跟主目標同排相鄰（見 engine/combat.ts ROW_NEIGHBORS，不跨排），退回主擊
  // 錨點時依濺射目標的站位名稱決定往左或往右偏移，視覺上至少落在正確的那一側；辨識不出左右
  // （例如未來站位命名改變）就不偏移，直接疊在主擊錨點上，總比完全不顯示好。
  const SPLASH_FALLBACK_OFFSET_PX = 36;
  const splashFallbackOffsetX = useCallback((slot: string | undefined): number => {
    if (!slot) return 0;
    if (slot.endsWith('_left')) return -SPLASH_FALLBACK_OFFSET_PX;
    if (slot.endsWith('_right')) return SPLASH_FALLBACK_OFFSET_PX;
    return 0;
  }, []);

  const statsRef = useRef<BattleStats>({ damageDealt: 0, damageTaken: 0, defeatedLevels: [], attacks: 0, chargedAttacks: 0, skillsUsed: 0, itemsUsed: 0, guardMs: 0 });
  // P2 遙測：engine 的 'attack' 事件普攻／技能傷害共用同一個 kind、不帶 skillId（見 engine/combat.ts
  // resolveAttackOrDamageSkill 同時被 dispatch.ts 的 ATTACK_RELEASE 與 combat.ts 的 resolveCastEffect
  // 呼叫）。玩家的傷害技能會先發一個 'skillCast' 事件（cast 完成後才真正結算傷害、補發 'attack'），
  // 這裡記「下一個屬於玩家的 attack 事件其實是這次技能的結算」，避免把它重複算進普攻次數；
  // heal/shield 技能各自有專屬事件（'heal'/'shield'），不會有後續的 'attack'，不需要設這個旗標。
  // 邊界情況：玩家在技能結算前死亡，pendingCasts 會被引擎靜默丟棄（tick.ts）、不會補發 'attack' 事件，
  // 這裡靠 'actorDown' 順手重置旗標，避免旗標卡 true 誤吃掉下一次真正的普攻統計。
  const pendingSkillDamageRef = useRef(false);

  // reportedRef 提前到這裡宣告（原本在 phase→'ended' 遙測 effect 正上方）——2026-09-14 P2 修正第1輪
  // 審查6 新增的「卸載時判定棄戰」effect 也需要用到它，而該 effect 位置在檔案較前段（音訊 effect
  // 旁），提前宣告避免用到尚未宣告的變數。battleStartAtRef 不能一起提前——它的初始值是 state.now，
  // 但 state 要等下面 `const battle = useBattle(...)` 之後才存在，故 battleStartAtRef 改宣告在
  // player 之後（見下方），只有 reportedRef（純 useRef(false)，不依賴 state）留在這裡。
  // P2 遙測共用旗標：「戰鬥結束」與「中途卸載視為棄戰」兩個 effect 都會嘗試送出一筆 report，
  // 這顆旗標確保無論哪一邊先發生，全場最多只送一次；也防 StrictMode 雙渲染造成的重複呼叫
  // （上層打 API 是否去重是上層的事，這裡只保證「只報一次」）。
  const reportedRef = useRef(false);

  // ---- 事件 → 效果：音效／特效／飄字／震動。engine 只負責「發生了什麼」，這裡負責「畫面怎麼演」。 ----
  function handleBattleEvents(events: BattleEvent[], next: BattleState) {
    // 審查#5：本次呼叫（同一批事件）裡最近一次成功算出錨點的主擊座標，供濺射事件查不到自己的
    // 錨點時當退路（見 pushEnemyFloat 的 fallbackAnchor 參數註解）。combat.ts resolveWeaponAttack
    // 保證同一次攻擊一定是「先 emit 主擊事件、才視 splashPct 補發濺射事件」，所以濺射永遠排在自己
    // 那次主擊之後，這裡由上往下掃事件陣列時，走到濺射事件當下這個變數一定已經是「這次攻擊」的值。
    let lastPrimaryAttackAnchor: { x: number; y: number } | null = null;
    for (const ev of events) {
      switch (ev.kind) {
        case 'attack': {
          // 玩家／隊友打敵人（含技能傷害）：特效座標＝目標怪物軀幹中心（BattleStageHandle.getEnemyAnchor）。
          //
          // DORPG P7（WIRE §引擎：「splash 用既有傷害事件加 splash: true 旗標供浮字區分」，見契約
          // dorpg_p7 CONTRACT.md §3 斧的濺射）：ENGINE 尚未在 engine/types.ts 的 'attack' 事件正式
          // 加這個欄位——這裡用結構型別安全讀取（缺欄位時 undefined，等同 false），不假設它一定
          // 存在，ENGINE 補上正式欄位後這行讀法不需要再改（見任務回報對 ENGINE 的需求）。
          const splash = (ev as unknown as { splash?: boolean }).splash === true;

          statsRef.current.damageDealt += ev.damage;
          // 濺射不是玩家「另外發動了一次攻擊」，只是同一次攻擊的附帶效果，不計進 attacks／
          // chargedAttacks 遙測，也不需要動 pendingSkillDamageRef（斧濺射只發生在普攻，見契約 §3）。
          if (!splash && ev.actorId === next.playerId) {
            if (pendingSkillDamageRef.current) {
              pendingSkillDamageRef.current = false; // 這一下是技能結算，已經在 'skillCast' 那筆算過 skillsUsed 了
            } else {
              statsRef.current.attacks += 1;
              if (ev.charged) statsRef.current.chargedAttacks += 1;
            }
          }

          if (splash) {
            // 濺射：只在被波及的怪物身上飄一個較小的傷害數字（見 FloatText.tsx 'splash' 語氣），
            // 不重播整套斬擊特效／音效——那些是「這次攻擊」本身的演出，濺射只是附帶效果，重播會
            // 讓同一次攻擊看起來像對三隻怪各自發動了一次獨立攻擊。
            // 審查#5：查不到濺射目標自己的錨點時，退回這次攻擊的主擊錨點＋依濺射目標站位左右偏移
            // （見上方 lastPrimaryAttackAnchor／splashFallbackOffsetX 註解）。
            const splashTarget = next.enemies.find((e) => e.id === ev.targetId);
            const fallbackAnchor = lastPrimaryAttackAnchor
              ? { x: lastPrimaryAttackAnchor.x + splashFallbackOffsetX(splashTarget?.slot), y: lastPrimaryAttackAnchor.y }
              : undefined;
            pushEnemyFloat(ev.targetId, `濺射 -${ev.damage}`, 'splash', true, fallbackAnchor);
          } else {
            const anchor = stageRef.current?.getEnemyAnchor(ev.targetId);
            if (anchor) {
              lastPrimaryAttackAnchor = anchor; // 給後面可能跟著來的濺射事件當退路基準（見上方註解）。
              void stageRef.current?.play({
                weapon: ev.weapon,
                result: ev.result,
                damage: ev.damage,
                x: anchor.x,
                y: anchor.y,
                targetId: ev.targetId,
              });
            }
            // miss/immune 的怪物「hit」動畫不會播（engine 只在 damage>0 時才把 anim 轉成
            // hitReaction，見 engine/combat.ts::applyEnemyDamage），這裡不必另外判斷，BattleStage
            // 直接照 enemy.anim 顯示。
            battleAudio.playSfx(`sfx_${ev.weapon}_${ev.result}`);
          }
          break;
        }
        case 'skillCast': {
          if (ev.actorId === next.playerId) {
            statsRef.current.skillsUsed += 1;
            const def = sample.skills.find((s) => s?.id === ev.skillId);
            if (def?.kind === 'damage') pendingSkillDamageRef.current = true;
          }
          break;
        }
        case 'enemyAttack': {
          // 全隊（含隊友）承受的傷害合計，跟 damageDealt 的「全隊造成」口徑對稱——見 BattleStats 型別註解。
          statsRef.current.damageTaken += ev.damage;
          // P2：怪物攻擊 miss 時 damage 恆為 0、不扣盾不扣血（engine/ai.ts 的 missChance 分支
          // 沒有呼叫 applyPartyDamage）——這裡只補畫面回饋，不震動（沒有真的受到傷害），
          // 沿用既有 FloatTextTone 的 'miss' 色調（灰），不用另外新增色票。
          if (ev.result === 'miss') {
            pushFloat(ev.targetId, 'Miss', 'miss');
          } else {
            pushFloat(ev.targetId, ev.guarded ? `防禦 -${ev.damage}` : `-${ev.damage}`, 'damage');
            if (ev.targetId === next.playerId && settingsRef.current.vibrate) battleAudio.vibrate([30]);
          }
          break;
        }
        case 'heal':
          pushFloat(ev.targetId, `+${ev.amount}`, 'heal');
          break;
        case 'shield':
          pushFloat(ev.targetId, `+${ev.amount} 護盾`, 'shield');
          break;
        case 'itemUsed': {
          // 引擎只有玩家能觸發 USE_ITEM（AI 隊友沒有這個指令），不必再篩 actorId。
          statsRef.current.itemsUsed += 1;
          const def = sample.items.find((it) => it.id === ev.itemId);
          const text = def?.kind === 'revive' ? `復活 ${ev.amount}%` : `+${ev.amount}`;
          pushFloat(ev.targetId, text, 'heal');
          break;
        }
        case 'enemyDeath': {
          const enemy = next.enemies.find((e) => e.id === ev.enemyId);
          if (enemy) statsRef.current.defeatedLevels.push(enemy.level);
          break;
        }
        case 'actorDown': {
          if (ev.actorId === next.playerId) pendingSkillDamageRef.current = false; // 見上面檔頭說明的邊界情況
          break;
        }
        // ---- P5 POLISH：special 拒絕、buff/debuff 套用與到期的畫面回饋（契約 §5，任務指示）。----
        case 'skillUnavailable':
          // engine/dispatch.ts 該事件的檔頭註解：這條路徑 actorId 恆為 player.id（只有玩家自己的
          // USE_SKILL 分支會拒絕 implemented=false 的技能，AI 隊友不會走到這裡），不必判斷 actorId。
          // 沿用既有 FloatText 樣式＋'miss' 色調（灰）——語意上跟「這個操作沒有效果」是同一件事。
          pushFloat(next.playerId, '尚未實裝', 'miss');
          break;
        case 'statusApplied': {
          // 箭頭方向只看數值正負（任務規格：正值↑負值↓），跟 tone（buff=藍／debuff=紅）是兩個獨立
          // 維度——例如「降低受到傷害」是 buff 但 value 是負的，會顯示「↓受到傷害」＋buff 色調。
          const arrow = ev.value >= 0 ? '↑' : '↓';
          const label = `${arrow}${skillStatLabel(ev.stat)}`;
          const tone: FloatTextTone = ev.effectKind === 'buff' ? 'buff' : 'debuff';
          if (next.party.some((p) => p.id === ev.targetId)) pushFloat(ev.targetId, label, tone);
          else pushEnemyFloat(ev.targetId, label, tone);
          break;
        }
        case 'statusExpired':
          // 標籤列（partyStatusTags／enemyTagOverlay，見下方）直接讀當下的 activeEffects 即時算，
          // 到期那一格下一次渲染自然消失；任務規格明講不必飄字，這裡刻意不做事。
          break;
        // escapeJudging/escapeFailed/escaped：TopBar 的訊息直接從 state.escape 算，不需要在這裡處理。
        default:
          break;
      }
    }
  }

  const battle = useBattle(sample, { config, onEvents: handleBattleEvents });
  const { state, send } = battle;
  const player = state.party[0]; // 契約：party[0] 恆為玩家（state.playerId 也指向它）。

  // battleStartAtRef 必須宣告在 state 之後（初始值取 state.now，即開戰那一刻的引擎時鐘）；
  // resultSummary／phase→'ended' 遙測 effect／下面新增的「卸載視為棄戰」effect 都會用到它。
  const battleStartAtRef = useRef(state.now); // useRef 只在首次 render 採用這個值，之後忽略——正好是開戰時刻

  // 除錯句柄：只在 ?dorpgDebug=1 時掛，供 verify 腳本讀狀態／直接送指令（不繞過引擎規則）。
  useEffect(() => {
    if (!dorpgDebug || typeof window === 'undefined') return;
    const w = window as unknown as { __dorpgBattle?: unknown };
    w.__dorpgBattle = { getState: () => state, send, sample };
    return () => {
      delete w.__dorpgBattle;
    };
  }, [dorpgDebug, state, send, sample]);

  // ---- P2 遙測：guard_ms（防禦累積時長）。engine 的 GUARD_BEGIN/GUARD_END 不發專屬事件（純狀態轉移），
  // 用 player.action 的轉場自己量：進 'guarding' 記起點，離開時（GUARD_END／或倒下打斷）用當下 state.now
  // 減起點累加。這個 effect 只依賴 player.action，一次轉場只會跑一次，state.now 用閉包裡「這次轉場當下
  // render」的值即可（不需要也不該列進 deps——列了只是額外空轉，因為只有 action 真的變了才需要重新配對）。
  const guardStartRef = useRef<number | null>(null);
  useEffect(() => {
    if (player.action === 'guarding') {
      guardStartRef.current = state.now;
    } else if (guardStartRef.current !== null) {
      statsRef.current.guardMs += state.now - guardStartRef.current;
      guardStartRef.current = null;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [player.action]);

  // ---- 音訊：每次手勢同步播 BGM（手勢堆疊）＋預載常用音效；離開整個戰鬥流程時才停 BGM。 ----
  // 2026-09-14 SCREENS 接線（AUDIO_CORE 根因修復：playBgmFromGesture 必須是手勢 handler 的第一行，
  // 前面不可有 await/then——原本的 `unlock().then(() => playBgm(...))` 結構會讓 play() 落在 await
  // 之後，脫離使用者手勢的同步呼叫堆疊，iOS Safari 一律拒絕，見 audio.ts 檔頭）。playBgmFromGesture
  // 對同一個 kind 重複呼叫是 no-op（不重播/不重置進度），每次呼叫成本可忽略，也讓「重試」自然發生在
  // 使用者之後的每一次操作上。preloadSfx 不需要手勢，仍可留在 unlock().then() 之後。
  // 2026-09-14 修復 C：同時掛在 pointerdown/pointerup/click 三種事件的 capture 階段（而非只有
  // pointerdown）——WebKit 對「哪種事件算合法使用者手勢」比 Chromium 嚴格，業界慣例是掛在手勢
  // *完成*的 click/pointerup，若只掛 pointerdown 這一種、剛好 iOS 不認，就會重演「每次重試都在同一
  // 個被拒模式裡打轉」。三個都掛、任何一個先觸發就先播，沒有副作用（同首不重播）。
  const handleRootPointerDownCapture = () => {
    battleAudio.playBgmFromGesture(sample.sceneKind === 'boss' ? 'boss' : 'master');
    void battleAudio.unlock().then(() => {
      void battleAudio.preloadSfx(PRELOAD_SFX_IDS);
    });
  };

  // phaseRef／onReportRef：讓下面「掛載/卸載各跑一次」的 effect（deps=[]，故意不列 phase/onReport，
  // 否則每次 phase 變動就會重新訂閱 suspendOnHidden／重播一次 BGM）在真正卸載那一刻仍能讀到*當下*
  // 的 phase／onReport，而不是掛載那一刻閉包住的舊值——沿用檔案裡 settingsRef 同一種手法。
  const phaseRef = useRef(state.phase);
  phaseRef.current = state.phase;
  const onReportRef = useRef(onReport);
  onReportRef.current = onReport;
  // 2026-09-14 P2 修正第1輪 審查6：卸載時若戰鬥尚未結束視為「棄戰」，見下面 effect 的 cleanup。
  // 用 setTimeout(0) 延後送出＋讓下一次 setup 取消它，藉此分辨「真的卸載」跟 React StrictMode 開發
  // 模式下的模擬卸載（同一個 fiber 的 mount→cleanup→mount 會在計時器觸發前的同一輪同步跑完）。
  const abandonTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => {
    if (abandonTimerRef.current !== null) {
      clearTimeout(abandonTimerRef.current);
      abandonTimerRef.current = null;
    }
    const cleanupHidden = battleAudio.suspendOnHidden();
    // 2026-09-14 SCREENS 接線：掛載當下就嘗試恢復/接續 BGM，不必等玩家在新實例點下第一個攻擊/道具
    // 鈕——「再戰一場」用 key={nonce} 整個卸載重掛（PhoneShell.tsx／Preview.tsx），觸發卸載的那次
    // 點擊（按在結算面板的「再戰一場」鈕上）發生在*舊*實例身上。這裡改呼叫非手勢版的 playBgm()（不
    // 再包 unlock().then()）：對已經在播的同一首（EncounterPicker 的 pointerdown 或前一場已經解鎖並
    // 播放過）是 no-op，銜接無縫；對尚未解鎖的情境（例如深連結直接開戰鬥）會嘗試同步 play()，若被
    // iOS 拒絕就交給上面 handleRootPointerDownCapture 的手勢入口重試。preloadSfx 不需要手勢，可直接呼叫。
    battleAudio.playBgm(sample.sceneKind === 'boss' ? 'boss' : 'master');
    void battleAudio.preloadSfx(PRELOAD_SFX_IDS);
    return () => {
      cleanupHidden();
      // 2026-09-14 SCREENS：不在這裡 stopBgm()——連續多場戰鬥（「再戰一場」的卸載重掛）不該斷音樂。
      // 停止 BGM 改由 PhoneShell／Preview 這層在「離開整個戰鬥流程」（battleView 變回 null／
      // view 離開 battle+picker）時呼叫，這裡卸載只清這個實例自己開的特效層與棄戰計時器。
      stageRef.current?.cancelAll();
      // 審查6（觀察→已修）：目前唯一已知會在 phase!=='ended' 時卸載 BattleScreen 的路徑是外層
      // 狀態被迫切走（例如 single-session-auth 踢除舊裝置、token 失效導致 PhoneShell 整層改渲染
      // 登入畫面），但這裡不假設「未來只會有這一種路徑」——只要不是正常打完（勝/敗/逃跑）就卸載，
      // 一律算棄戰，讓後台「戰鬥數據」看得到玩家半途而廢的比例，不然這種場次直接消失、分母只剩
      // 打完的場次，勝率會被高估（見 review/data.md 佐證）。reportedRef 跟 phase→'ended' 的遙測
      // 共用同一顆旗標防重複：如果已經正常結算並回報過，這裡就不會再送第二筆。
      if (phaseRef.current !== 'ended' && !reportedRef.current) {
        abandonTimerRef.current = setTimeout(() => {
          abandonTimerRef.current = null;
          if (reportedRef.current) return;
          reportedRef.current = true;
          onReportRef.current?.({
            encounterCode: encounter.code,
            outcome: 'abandoned',
            durationMs: Math.max(0, performance.now() - battleStartAtRef.current),
            damageDealt: statsRef.current.damageDealt,
            damageTaken: statsRef.current.damageTaken,
            enemiesDefeated: statsRef.current.defeatedLevels.length,
            attacks: statsRef.current.attacks,
            chargedAttacks: statsRef.current.chargedAttacks,
            skillsUsed: statsRef.current.skillsUsed,
            itemsUsed: statsRef.current.itemsUsed,
            guardMs: statsRef.current.guardMs,
          });
        }, 0);
      }
    };
    // 只在掛載/卸載各跑一次：suspendOnHidden／playBgm／cancelAll 都是單例/imperative API，不依賴 render
    // 值；phase/onReport 一律經 ref 讀取（見上面兩顆 ref 的說明），故意不列進 deps。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const handleBack = useCallback(() => {
    // 2026-09-14 SCREENS：不在這裡 stopBgm()——「關閉結算面板」通常還留在戰鬥流程內（PhoneShell
    // 回角色頁前的 onExit、Preview 回選單的 onNext 都算，音樂應該連續），交給 PhoneShell／Preview
    // 依「離開整個戰鬥流程」與否決定要不要停。
    stageRef.current?.cancelAll();
    onBack();
  }, [onBack]);

  // ---- 蓄氣條：逐幀讀 battle.liveStateRef（不經過引擎重算，只是讀數），只在充能中才跑這個迴圈。 ----
  const isCharging = player.action === 'charging';
  const [chargeDisplay, setChargeDisplay] = useState(0);
  useEffect(() => {
    if (!isCharging) {
      setChargeDisplay(0);
      return;
    }
    let raf = 0;
    const loop = () => {
      const live = battle.liveStateRef.current;
      const p = live.party.find((pp) => pp.isPlayer);
      if (!p || p.action !== 'charging' || p.chargeStartedAt === null) return; // 已放開/取消，effect cleanup 隨後接手
      // INTEGRATOR 補（ENGINE 需求：「巨劍的蓄氣條 UI 進度暫時不會反映拉長的蓄氣時間」）：補上第三
      // 參數 chargeTimeMul，讓蓄氣條的視覺進度跟引擎判定的實際蓄氣時間一致（沒有武器／非巨劍時
      // p.weaponProfile 為 null 或 chargeTimeMul=1，跟原本行為零改動）。
      setChargeDisplay(engineChargeRatio(performance.now() - p.chargeStartedAt, live.config, p.weaponProfile?.chargeTimeMul ?? 1));
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, [isCharging, battle.liveStateRef]);

  // ---- 冷卻倒數：攻擊鈕＋每個裝備技能，只在顯示的整秒數變化時才 setState（沿用 P0 demo 冷卻的節流手法）。 ----
  const [cooldownDisplay, setCooldownDisplay] = useState<{ attackMs: number; skills: Record<string, number> }>({ attackMs: 0, skills: {} });
  useEffect(() => {
    if (state.phase !== 'active') return;
    let raf = 0;
    let lastKey = '';
    const loop = () => {
      const live = battle.liveStateRef.current;
      const now = performance.now();
      const p = live.party.find((pp) => pp.isPlayer);
      const attackMs = p ? Math.max(0, p.attackReadyAt - now) : 0;
      const skills: Record<string, number> = {};
      for (const sk of live.skills) {
        if (!sk) continue;
        skills[sk.id] = Math.max(0, (live.skillReadyAt[sk.id] ?? 0) - now);
      }
      const key = `${Math.ceil(attackMs / 1000)}|${Object.keys(skills)
        .map((id) => `${id}:${Math.ceil(skills[id] / 1000)}`)
        .join(',')}`;
      if (key !== lastKey) {
        lastKey = key;
        setCooldownDisplay({ attackMs, skills });
      }
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, [state.phase, battle.liveStateRef]);

  // ---- 版面（沿用 P0 規則，未改動） ----
  const measured = size.w > 0 && size.h > 0;
  const w = size.w;
  const h = size.h;
  const tier = layoutFor(h);
  const bands = LAYOUT[tier];
  const stageH = Math.max(MIN_STAGE_H, sceneHeightFor(h, tier));
  const scrollable = h < SCROLL_MIN_H || w < SCROLL_MIN_W;
  const cardW = Math.min((w - PARTY_PAD_X * 2 - PARTY_GAP * (PARTY_COUNT - 1)) / PARTY_COUNT, bands.party / 2);

  // ---- 引擎狀態 → 子元件 props 的投影（皆以 useMemo 鎖住 identity，讓上面的 memo 包裝有意義） ----
  const stageEnemies: StageEnemy[] = useMemo(
    () =>
      state.enemies.map((e) => ({
        id: e.id,
        name: e.name,
        // DORPG P6（契約 §1）：Math.floor 保險，同 toPartyMemberView 上方註解——EnemyPlate 內部
        // 也會再 floor 一次，這裡先整數化是為了 stageEnemies 這份 useMemo 投影本身就該是乾淨資料。
        level: Math.floor(e.level),
        hp: Math.floor(e.hp),
        hpMax: Math.floor(e.stats.hpMax),
        slot: e.slot,
        imageUrl: e.imageUrl,
        anim: e.anim,
        rank: e.rank,
        attribute: e.attribute,
        size: e.size,
        race: e.race,
      })),
    [state.enemies],
  );
  const target = useMemo(() => state.enemies.find((e) => e.id === state.targetId) ?? null, [state.enemies, state.targetId]);
  const partyViews = useMemo(() => state.party.map(toPartyMemberView), [state.party]);
  // P5 POLISH：隊員身上目前生效的 buff/debuff（PartyCard 標籤列用），只投影 stat/value 兩個欄位。
  const partyStatusTags = useMemo(
    () => state.party.map((p) => p.activeEffects.map((e): { stat: BuffDebuffStat; value: number } => ({ stat: e.stat, value: e.value }))),
    [state.party],
  );
  // P5 POLISH：敵人身上的 buff/debuff 標籤列——EnemyPlate 本體定義在 BattleStage.tsx（本輪寫入範圍
  // 不含該檔），改在這裡重算一次站位（同 BattleStage.tsx computeEnemyPlacement 的 footX/footY 公式，
  // 見 PLATE_LIFT_DUP 常數註解）疊一層獨立 overlay，只加不改 BattleStage 既有的渲染樹。
  const enemyTagOverlay = useMemo(() => {
    const out: Record<string, { left: number; top: number; width: number; fontSize: number; text: string }> = {};
    if (!measured) return out;
    const k = w / LAYOUT.designWidth;
    for (const e of state.enemies) {
      if (e.anim === 'removed' || e.activeEffects.length === 0) continue;
      const slot = sample.scene.slots.find((sl) => sl.id === e.slot);
      if (!slot) continue;
      const bandH = Math.min(stageH, SCENE_VIEWPORT.h * k);
      const footX = slot.x * w;
      const footY = stageH - (1 - slot.y) * bandH;
      const plateW = ENEMY_PLATE.w * k;
      const tags = e.activeEffects.slice(0, 3).map((eff) => `${eff.value >= 0 ? '↑' : '↓'}${skillStatLabel(eff.stat)}`);
      const extra = e.activeEffects.length > 3 ? ` +${e.activeEffects.length - 3}` : '';
      out[e.id] = {
        left: Math.round(footX - plateW / 2),
        // 貼在 EnemyPlate 上緣（footY − PLATE_LIFT_DUP×k）再留 2px 縫；實際渲染再疊
        // transform:translateY(-100%) 讓文字整塊往上長，不必事先知道自己的行高。
        top: Math.round(footY - PLATE_LIFT_DUP * k - 2),
        width: Math.round(plateW),
        fontSize: Math.max(8, Math.round(9 * k)),
        text: tags.join(' ') + extra,
      };
    }
    return out;
  }, [measured, state.enemies, sample.scene.slots, w, stageH]);
  // phase !== 'active'（resolving：勝負已定但最後一隻怪的死亡動畫還沒播完；ended：已結算）一律鎖死互動——
  // engine 的 dispatch.ts 本來就會在非 active 時直接拒絕所有指令（純防禦性，不影響正確性），這裡要做的
  // 是「UI 看起來也對得上」：指令鈕全部 disabled、隊員卡不可點；trayMode/targeting 兩個欄位不動它，
  // 維持 engine 目前的值原樣顯示（不是本檔的職責去重置它們）。
  const isInactive = state.phase !== 'active';
  const targetableFlags = useMemo(() => {
    if (isInactive) return state.party.map(() => false);
    const t = state.targeting;
    if (t.mode !== 'chooseAlly') return state.party.map(() => false);
    if (t.skillId) return state.party.map((actor) => actor.hp > 0);
    if (t.itemId) {
      const entry = state.items.find((i) => i.def.id === t.itemId);
      const isRevive = entry?.def.kind === 'revive';
      return state.party.map((actor) => (isRevive ? actor.hp <= 0 : actor.hp > 0));
    }
    return state.party.map(() => false);
  }, [isInactive, state.targeting, state.items, state.party]);
  const trayItems: Item[] = useMemo(() => state.items.map((i) => ({ ...i.def, quantity: i.quantity })), [state.items]);
  const unavailableIds = useMemo(() => {
    const s = new Set<string>();
    const busy = isInactive || player.action !== 'idle';
    if (state.trayMode === 'skills') {
      for (const sk of state.skills) {
        if (!sk) continue;
        if (busy || player.mp < sk.mpCost) s.add(sk.id);
      }
    } else if (busy) {
      for (const it of state.items) s.add(it.def.id);
    }
    return s;
  }, [isInactive, state.trayMode, state.skills, state.items, player.action, player.mp]);
  const selectedTrayId = state.targeting.mode === 'chooseAlly' ? state.targeting.skillId ?? state.targeting.itemId ?? null : null;

  // ---- 指令：把使用者手勢翻譯成 Command（規則本身一律由 engine 判斷/拒絕，這裡不重複檢查）。 ----
  const handleAllyPick = useCallback(
    (actorId: string) => {
      if (isInactive) return;
      const t = state.targeting;
      if (t.mode !== 'chooseAlly') return;
      if (t.skillId) send({ type: 'USE_SKILL', skillId: t.skillId, targetId: actorId });
      else if (t.itemId) send({ type: 'USE_ITEM', itemId: t.itemId, targetId: actorId });
    },
    [isInactive, state.targeting, send],
  );
  // 逐槽位固定的零參數 callback：identity 只在 party 陣列或 handleAllyPick 真的變動時才變，
  // 讓 MemoPartyCard 在高頻的蓄氣/冷卻 tick 期間可以整組沿用舊 props、不重繪。
  const partyPickHandlers = useMemo(() => state.party.map((actor) => () => handleAllyPick(actor.id)), [state.party, handleAllyPick]);
  const partySelectHandlers = useMemo(() => state.party.map((_actor, i) => () => setMemberIdx(i)), [state.party.length]);

  const handleTrayPick = (id: string) => {
    if (state.trayMode === 'skills') {
      if (state.targeting.mode === 'chooseAlly' && state.targeting.skillId === id) {
        send({ type: 'CANCEL_TARGETING' });
        return;
      }
      send({ type: 'USE_SKILL', skillId: id });
      return;
    }
    if (state.targeting.mode === 'chooseAlly' && state.targeting.itemId === id) {
      send({ type: 'CANCEL_TARGETING' });
      return;
    }
    send({ type: 'USE_ITEM', itemId: id });
  };
  const toggleItems = () => {
    send({ type: 'SET_TRAY', mode: state.trayMode === 'items' ? 'skills' : 'items' });
    setTrayOffset(0);
  };
  const handleSelectTarget = useCallback((enemyId: string) => send({ type: 'SELECT_TARGET', enemyId }), [send]);
  const handleBackgroundClick = useCallback(() => send({ type: 'CANCEL_TARGETING' }), [send]);

  // ---- 按鈕狀態（契約 §7 BtnState 對應表；規則照 dispatch.ts 實際會拒絕的條件推導，不是憑感覺猜的） ----
  // isInactive（phase !== 'active'）取代原本只看 isEnded：resolving 這個過渡態（勝負已定、最後一隻怪
  // 死亡動畫還沒播完）也要讓所有指令鈕看起來是鎖住的，不是等到真正 ended 才鎖。
  const isDead = player.action === 'dead';
  const isGuarding = player.action === 'guarding';
  const itemState: BtnState = isDead || isInactive || isGuarding ? 'disabled' : state.trayMode === 'items' ? 'active' : 'normal';
  const guardState: BtnState = isDead || isInactive ? 'disabled' : isGuarding ? 'pressed' : 'normal';
  const attackState: BtnState = isDead || isInactive || isGuarding ? 'disabled' : isCharging ? 'pressed' : 'normal';

  const attackHold = useHoldGesture(
    {
      start: () => send({ type: 'ATTACK_BEGIN' }),
      release: () => send({ type: 'ATTACK_RELEASE' }),
      cancel: () => send({ type: 'HOLD_CANCEL' }),
    },
    { disabled: attackState === 'disabled' },
  );
  const guardHold = useHoldGesture(
    {
      start: () => send({ type: 'GUARD_BEGIN' }),
      release: () => send({ type: 'GUARD_END' }),
      cancel: () => send({ type: 'GUARD_END' }), // 防禦沒有蓄力機制，取消＝直接結束防禦，避免卡在 guarding
    },
    { disabled: guardState === 'disabled' },
  );

  // ---- 逃跑鈕狀態／訊息：judging 沒有專屬事件文字（engine 只在判定「結束」時寫 message），
  // 判定中的「判定中…」由這裡補上；其餘直接沿用 engine 算好的 escape.message。 ----
  const escapeState: EscapeState = !isInactive && state.escape.flow === 'available' ? 'normal' : 'disabled';
  const escapeMessage = state.escape.flow === 'judging' ? '判定中…' : state.escape.message || undefined;

  // ---- 結算摘要（ResultOverlay）：擊敗數／總傷害用 statsRef 全場累加；經驗值為示範值，非正式數值。
  // battleStartAtRef 已提前到上面宣告（見該處註解）。----
  const resultSummary = useMemo(() => {
    const defeatedLevels = statsRef.current.defeatedLevels;
    return {
      enemiesDefeated: defeatedLevels.length,
      damageDealt: statsRef.current.damageDealt,
      durationMs: Math.max(0, state.now - battleStartAtRef.current),
      // 示範值：擊敗敵人等級合計 × 3（規格未定正式經驗公式，見回報）。
      expPreview: defeatedLevels.reduce((a, b) => a + b, 0) * 3,
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.phase, state.now]);

  // ---- P2 遙測：戰鬥結束（phase→'ended'）就把累積統計交給上層 onReport。reportedRef 已提前到上面
  // 宣告（與「中途卸載視為棄戰」的 effect 共用，見該處註解）。----
  useEffect(() => {
    if (state.phase !== 'ended' || !state.outcome || reportedRef.current) return;
    reportedRef.current = true;
    onReport?.({
      encounterCode: encounter.code,
      outcome: state.outcome,
      durationMs: resultSummary.durationMs,
      damageDealt: statsRef.current.damageDealt,
      damageTaken: statsRef.current.damageTaken,
      enemiesDefeated: statsRef.current.defeatedLevels.length,
      attacks: statsRef.current.attacks,
      chargedAttacks: statsRef.current.chargedAttacks,
      skillsUsed: statsRef.current.skillsUsed,
      itemsUsed: statsRef.current.itemsUsed,
      guardMs: statsRef.current.guardMs,
    });
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.phase, state.outcome]);

  // 顏色只從 PALETTE 來（根節點固定 data-skin="default" 暗色，不讀前台 skin 變數）；CSS 只透過這些自訂變數取色。
  const rootStyle = {
    background: PALETTE.surfaceBase,
    color: PALETTE.textPrimary,
    '--c-gold': PALETTE.borderGold,
    '--c-panel': PALETTE.surfacePanel,
    '--c-text': PALETTE.textPrimary,
    '--c-muted': PALETTE.textSecondary,
    '--c-focus': PALETTE.targetGold,
    '--c-disabled': PALETTE.disabledText,
  } as CSSProperties;

  return (
    <div
      ref={rootRef}
      className={styles.root}
      style={rootStyle}
      data-skin="default"
      data-reduce-motion={settings.reduceMotion ? 'true' : undefined}
      onPointerDownCapture={handleRootPointerDownCapture}
      onPointerUpCapture={handleRootPointerDownCapture}
      onClickCapture={handleRootPointerDownCapture}
    >
      {measured && (
        <div className={scrollable ? `${styles.column} ${styles.scrollable}` : styles.column} style={{ width: w }}>
          <div className={styles.band}>
            <MemoTopBar escapeState={escapeState} escapeMessage={escapeMessage} onEscape={() => send({ type: 'TRY_ESCAPE' })} onSettings={() => setShowSettings(true)} height={bands.toolbar} />
          </div>

          <div
            className={`${styles.band} ${styles.party}`}
            style={{ height: bands.party, padding: `0 ${PARTY_PAD_X}px`, gap: PARTY_GAP }}
            role="group"
            aria-label="隊伍"
          >
            {Array.from({ length: PARTY_COUNT }, (_, i) => (
              <MemoPartyCard
                key={state.party[i]?.id ?? `empty-${i}`}
                member={partyViews[i] ?? null}
                selected={i === memberIdx}
                // isInactive：隊員卡不可點（見上面 isInactive 的說明）——不傳回呼即可讓 PartyCard 的
                // handleClick 拿到 undefined，點下去自然沒有任何反應，不需要改 PartyCard.tsx 本身。
                onSelect={isInactive ? undefined : partySelectHandlers[i]}
                width={cardW}
                targetable={targetableFlags[i] ?? false}
                onPick={isInactive ? undefined : partyPickHandlers[i]}
                floatText={state.party[i] ? floatTexts[state.party[i].id] : undefined}
                statusTags={partyStatusTags[i]}
              />
            ))}
          </div>

          <div className={styles.band} style={{ height: stageH }}>
            <MemoBattleStage
              ref={stageRef}
              scene={sample.scene}
              enemies={stageEnemies}
              targetId={state.targetId}
              onSelect={handleSelectTarget}
              onBackgroundClick={handleBackgroundClick}
              width={w}
              height={stageH}
              reducedMotion={settings.reduceMotion}
            />
            {/* P5 POLISH：敵人 buff/debuff 標籤列——見 enemyTagOverlay 的站位公式與寫入範圍限制說明。
                純疊圖、不吃點擊，才不會擋到 BattleStage 自己的怪物選取按鈕。 */}
            {Object.entries(enemyTagOverlay).map(([id, t]) => (
              <div
                key={id}
                className={enemyPlateStyles.tagLine}
                style={{ left: t.left, top: t.top, width: t.width, transform: 'translateY(-100%)', fontSize: t.fontSize, color: PALETTE.textPrimary }}
              >
                {t.text}
              </div>
            ))}
            {/* P5 POLISH：debuff 命中敵人時的飄字（定點模式，見 FloatText.tsx anchorPx）。
                DORPG P7：斧的濺射傷害（tone='splash'）也走這個容器，small 由 pushEnemyFloat 帶入。 */}
            {Object.entries(enemyFloatTexts).map(([id, ft]) => (
              <FloatText key={`${id}-${ft.key}`} text={ft.text} tone={ft.tone} anchorPx={{ x: ft.x, y: ft.y }} reducedMotion={settings.reduceMotion} small={ft.small} />
            ))}
          </div>

          <div className={`${styles.band} ${styles.targetRow}`} style={{ height: bands.target }}>
            <div style={{ width: w - TARGET_INSET }}>
              <MemoTargetBar target={target ? { name: target.name, level: target.level } : null} height={bands.target} />
            </div>
          </div>

          <div className={styles.band} style={{ height: bands.tray }}>
            <SkillTray
              mode={state.trayMode}
              skills={state.skills}
              items={trayItems}
              offset={trayOffset}
              onOffset={setTrayOffset}
              cooldownMs={cooldownDisplay.skills}
              unavailableIds={unavailableIds}
              selectedId={selectedTrayId}
              onPick={handleTrayPick}
              width={w}
              height={bands.tray}
            />
          </div>

          <div className={styles.band} style={{ height: bands.commands }}>
            <CommandBar
              item={itemState}
              guard={guardState}
              attack={attackState}
              onItem={toggleItems}
              charge={isCharging ? chargeDisplay : undefined}
              attackCooldown={{ remainingMs: cooldownDisplay.attackMs, totalMs: state.config.attackCooldownMs }}
              holdProps={{ attack: attackHold, guard: guardHold }}
              width={w}
              height={bands.commands}
            />
          </div>
        </div>
      )}

      {measured && showSettings && (
        <SettingsSheet width={w} values={settings} onChange={setSettings} onClose={() => setShowSettings(false)} />
      )}

      {state.phase === 'ended' && state.outcome && (
        // ResultOverlay 自己的 .overlay 刻意不設 z-index（契約 §6：z-index 由這裡決定），沒有這層
        // wrapper 會被上面各 band 的 z-index:100 蓋住，畫面上完全看不到（DOM 查詢/文字斷言仍會過，
        // 是純視覺 bug——2026-09-14 CDP 截圖走查抓到，見 .module.css 的 .resultLayer 說明）。
        <div className={styles.resultLayer}>
          <ResultOverlay outcome={state.outcome} summary={resultSummary} onClose={handleBack} onRestart={onRestart} onPickAnother={onNext} />
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------------
// 設定面板：底部抽屜，frame_dialog 九宮格當面板底；音樂／音效滑桿直接接 battleAudio（立即生效＋持久化），
// 震動開關存 localStorage 'dorpg.vibe'，減少動態純本機狀態（沿用 P0，未持久化——契約沒要求）。
// ---------------------------------------------------------------------------
type SettingsSheetProps = {
  width: number;
  values: SettingsValues;
  onChange: (next: SettingsValues) => void;
  onClose: () => void;
};

function SettingsSheet({ width, values, onChange, onClose }: SettingsSheetProps) {
  // Esc 關閉（桌機預覽方便）；點遮罩亦關閉。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  const divider = KIT_SIZES.ornament_divider;

  return (
    <div className={styles.overlay}>
      <button type="button" className={styles.scrim} style={{ background: PALETTE.overlayScrim }} aria-label="關閉設定" onClick={onClose} />
      <div className={styles.sheet} style={{ width }} role="dialog" aria-modal="true" aria-labelledby="dorpg-settings-title">
        {/* frame_dialog 九宮格：border 16 + padding 4 = manifest contentRect 內縮 20 */}
        <div className={styles.sheetPanel} style={nineSliceStyle('frame_dialog')}>
          <div className={styles.sheetHead}>
            <h2 id="dorpg-settings-title" className={styles.sheetTitle}>
              設定
            </h2>
            <button type="button" className={styles.iconBtn} style={{ width: ICON_CLOSE, height: ICON_CLOSE }} onClick={onClose} aria-label="關閉">
              {/* eslint-disable-next-line @next/next/no-img-element */}
              <img src={kitAsset('icon_close')} width={ICON_CLOSE} height={ICON_CLOSE} alt="" draggable={false} />
            </button>
          </div>
          {/* eslint-disable-next-line @next/next/no-img-element */}
          <img className={styles.divider} src={kitAsset('ornament_divider')} width={divider.w} height={divider.h} alt="" draggable={false} />

          <label className={styles.row}>
            <span className={styles.rowLabel}>音樂</span>
            <input
              className={styles.slider}
              type="range"
              min={0}
              max={100}
              value={values.music}
              onChange={(e) => {
                const v = Number(e.target.value);
                battleAudio.setMusicVolume(v);
                onChange({ ...values, music: v });
              }}
              style={{ accentColor: PALETTE.borderGold }}
            />
            <output className={styles.rowValue}>{values.music}</output>
          </label>
          <label className={styles.row}>
            <span className={styles.rowLabel}>音效</span>
            <input
              className={styles.slider}
              type="range"
              min={0}
              max={100}
              value={values.sfx}
              onChange={(e) => {
                const v = Number(e.target.value);
                battleAudio.setSfxVolume(v);
                onChange({ ...values, sfx: v });
              }}
              style={{ accentColor: PALETTE.borderGold }}
            />
            <output className={styles.rowValue}>{values.sfx}</output>
          </label>
          <div className={styles.row}>
            <span id="dorpg-set-vibrate" className={styles.rowLabel}>
              震動
            </span>
            <Switch
              on={values.vibrate}
              labelledBy="dorpg-set-vibrate"
              onToggle={() => {
                const next = !values.vibrate;
                writeVibeSetting(next);
                onChange({ ...values, vibrate: next });
              }}
            />
          </div>
          <div className={styles.row}>
            <span id="dorpg-set-motion" className={styles.rowLabel}>
              減少動態
            </span>
            <Switch on={values.reduceMotion} labelledBy="dorpg-set-motion" onToggle={() => onChange({ ...values, reduceMotion: !values.reduceMotion })} />
          </div>

          <button type="button" className={styles.closeText} onClick={onClose}>
            關閉
          </button>
        </div>
      </div>
    </div>
  );
}

function Switch({ on, labelledBy, onToggle }: { on: boolean; labelledBy: string; onToggle: () => void }) {
  return (
    <button type="button" role="switch" aria-checked={on} aria-labelledby={labelledBy} className={styles.switch} data-on={on ? 'true' : undefined} onClick={onToggle}>
      <span className={styles.knob} />
    </button>
  );
}
