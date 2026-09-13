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
import type { BattleSample, BtnState, EscapeState, Item, PartyMember, TrayMode } from '@/lib/dorpg/types';
import { KIT_SIZES, LAYOUT, PALETTE, kitAsset, layoutFor, nineSliceStyle, sceneHeightFor } from '@/lib/dorpg/assets';
import { SAMPLE_BATTLE } from '@/lib/dorpg/sampleBattle';
import TopBar from './TopBar';
import PartyCard from './PartyCard';
import BattleStage, { type BattleStageHandle, type StageEnemy } from './BattleStage';
import TargetBar from './TargetBar';
import SkillTray from './SkillTray';
import CommandBar from './CommandBar';
import ResultOverlay from './ResultOverlay';
import type { FloatTextTone } from './FloatText';
import styles from './BattleScreen.module.css';
import { useBattle } from '@/lib/dorpg/useBattle';
import { chargeRatio as engineChargeRatio } from '@/lib/dorpg/engine';
import type { BattleEvent, BattleState, PartyActor } from '@/lib/dorpg/engine';
import { useHoldGesture } from '@/lib/dorpg/useHoldGesture';
import { battleAudio } from '@/lib/dorpg/audio';

export type BattleScreenProps = {
  onBack: () => void;
  /** 未給時用 SAMPLE_BATTLE（kit 測試資料＋content pack 素材）。 */
  sample?: BattleSample;
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
 *  只是 hpMax/mpMax 在引擎那邊收在 stats 裡——跟 MonsterSprite 的 adaptAnimSet 是同一種轉接手法。 */
function toPartyMemberView(actor: PartyActor): PartyMember {
  return {
    id: actor.id,
    name: actor.name,
    level: actor.level,
    hp: actor.hp,
    hpMax: actor.stats.hpMax,
    mp: actor.mp,
    mpMax: actor.stats.mpMax,
    portraitUrl: actor.portraitUrl,
  };
}

type SettingsValues = { music: number; sfx: number; vibrate: boolean; reduceMotion: boolean };

/** 累積整場戰鬥的統計數字（跨事件累加，不屬於引擎狀態——引擎只在乎「現在」，摘要是 UI 自己記的）。 */
type BattleStats = { damageDealt: number; defeatedLevels: number[] };

// 高頻更新（蓄氣條／冷卻倒數）只會改變 CommandBar/SkillTray 的 props，其餘子樹用 memo 包起來，
// 讓它們在那些 tick 期間直接沿用舊渲染結果，不必每幀重新計算整棵 PartyCard×5／BattleStage。
const MemoPartyCard = memo(PartyCard);
const MemoBattleStage = memo(BattleStage);
const MemoTargetBar = memo(TargetBar);
const MemoTopBar = memo(TopBar);

export default function BattleScreen({ onBack, sample: sampleProp = SAMPLE_BATTLE, debugAllowed = false }: BattleScreenProps) {
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
  const statsRef = useRef<BattleStats>({ damageDealt: 0, defeatedLevels: [] });

  // ---- 事件 → 效果：音效／特效／飄字／震動。engine 只負責「發生了什麼」，這裡負責「畫面怎麼演」。 ----
  function handleBattleEvents(events: BattleEvent[], next: BattleState) {
    for (const ev of events) {
      switch (ev.kind) {
        case 'attack': {
          // 玩家／隊友打敵人（含技能傷害）：特效座標＝目標怪物軀幹中心（BattleStageHandle.getEnemyAnchor）。
          statsRef.current.damageDealt += ev.damage;
          const anchor = stageRef.current?.getEnemyAnchor(ev.targetId);
          if (anchor) {
            void stageRef.current?.play({
              weapon: ev.weapon,
              result: ev.result,
              damage: ev.damage,
              x: anchor.x,
              y: anchor.y,
              targetId: ev.targetId,
            });
          }
          // miss/immune 的怪物「hit」動畫不會播（engine 只在 damage>0 時才把 anim 轉成 hitReaction，
          // 見 engine/combat.ts::applyEnemyDamage），這裡不必另外判斷，BattleStage 直接照 enemy.anim 顯示。
          battleAudio.playSfx(`sfx_${ev.weapon}_${ev.result}`);
          break;
        }
        case 'enemyAttack': {
          pushFloat(ev.targetId, ev.guarded ? `防禦 -${ev.damage}` : `-${ev.damage}`, 'damage');
          if (ev.targetId === next.playerId && settingsRef.current.vibrate) battleAudio.vibrate([30]);
          break;
        }
        case 'heal':
          pushFloat(ev.targetId, `+${ev.amount}`, 'heal');
          break;
        case 'shield':
          pushFloat(ev.targetId, `+${ev.amount} 護盾`, 'shield');
          break;
        case 'itemUsed': {
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
        // actorDown：卡片的「倒下」覆層由 hp<=0 直接算出（PartyCard 自己判斷），不需要另外處理。
        // escapeJudging/escapeFailed/escaped：TopBar 的訊息直接從 state.escape 算，不需要在這裡處理。
        default:
          break;
      }
    }
  }

  const battle = useBattle(sample, { onEvents: handleBattleEvents });
  const { state, send } = battle;
  const player = state.party[0]; // 契約：party[0] 恆為玩家（state.playerId 也指向它）。

  // 除錯句柄：只在 ?dorpgDebug=1 時掛，供 verify 腳本讀狀態／直接送指令（不繞過引擎規則）。
  useEffect(() => {
    if (!dorpgDebug || typeof window === 'undefined') return;
    const w = window as unknown as { __dorpgBattle?: unknown };
    w.__dorpgBattle = { getState: () => state, send, sample };
    return () => {
      delete w.__dorpgBattle;
    };
  }, [dorpgDebug, state, send, sample]);

  // ---- 音訊：每次 pointerdown 解鎖＋開 BGM＋預載常用音效；卸載/離開時停 BGM＋清特效。 ----
  // 2026-09-14 審查修正：原本用 audioUnlockedRef 只在第一次 pointerdown 嘗試一次，若當下 unlock()/
  // el.play() 被瀏覽器拒絕（常見於 iOS Safari 對第一個手勢的判定），之後就永遠沒有機會重試、整場靜音。
  // audio.ts 沒有暴露「BGM 是否真的在播」的查詢方法（也不在本輪可改檔案清單內，不能加），改用最簡單
  // 可靠的做法：不設鎖，每次 pointerdown（capture）都重新呼叫——unlock() 對已 resume 的 AudioContext
  // 是 no-op，playBgm() 對同一個 kind 重複呼叫也只是確保在播（見 audio.ts 註解：不重播/不重置進度），
  // preloadSfx() 對已快取的 id 直接命中快取，三者皆冪等、呼叫成本可忽略，藉此讓「重試」自然發生在使用者
  // 之後的每一次操作上，不需要額外偵測播放是否成功。
  const handleRootPointerDownCapture = () => {
    void battleAudio.unlock().then(() => {
      battleAudio.playBgm(sample.sceneKind === 'boss' ? 'boss' : 'master');
      void battleAudio.preloadSfx(PRELOAD_SFX_IDS);
    });
  };
  useEffect(() => {
    const cleanupHidden = battleAudio.suspendOnHidden();
    return () => {
      cleanupHidden();
      battleAudio.stopBgm();
      stageRef.current?.cancelAll();
    };
    // 只在掛載/卸載各跑一次：suspendOnHidden／stopBgm／cancelAll 都是單例/imperative API，不依賴 render 值。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const handleBack = useCallback(() => {
    battleAudio.stopBgm();
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
      setChargeDisplay(engineChargeRatio(performance.now() - p.chargeStartedAt, live.config));
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
        level: e.level,
        hp: e.hp,
        hpMax: e.stats.hpMax,
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

  // ---- 結算摘要（ResultOverlay）：擊敗數／總傷害用 statsRef 全場累加；經驗值為示範值，非正式數值。 ----
  const battleStartAtRef = useRef(state.now); // useRef 只在首次 render 採用這個值，之後忽略——正好是開戰時刻
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
          <ResultOverlay outcome={state.outcome} summary={resultSummary} onClose={handleBack} />
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
