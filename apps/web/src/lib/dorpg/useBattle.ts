'use client';

// DORPG P1 組裝層：把純函式引擎（@/lib/dorpg/engine）包成 React hook。
// 設計重點（契約 §7）：
// - useReducer 只是拿來當「有身份的 setState」使用（reducer 本身不做邏輯，真正的狀態機在 engine 裡）；
//   真正的規則、AI、計時全部委派給 dispatch()/tick()，這裡只負責「什麼時候呼叫它們、要不要重繪」。
// - rAF 迴圈只在 phase 'active'/'resolving' 時跑；tab 切到背景時跳過呼叫 tick（不产生新的計時推進，
//   回前景後從目前 wall-clock 時間繼續，可能因為背景期間完全沒 tick 而讓下一次 tick 一口氣往前跳一大段
//   ——這是 engine 本身「每次呼叫只推進一步狀態機」設計下無法避免的邊界情況，非本檔要解的問題，
//   本檔只負責契約明講的「document.hidden → 送 HOLD_CANCEL/GUARD_END 並暫停；回前景恢復」）。
// - 效能：tick() 每次呼叫都會回傳一份新物件（toCtx/fromCtx 的設計，即使什麼都沒變也是新 identity），
//   若每幀都 setState 會讓整棵 BattleScreen 樹以 60fps 重繪。rAF 迴圈裡只在「UI 簽章」變化時才
//   setState（見 tickUiSignature：seq + 玩家 action + phase + escape.flow + targeting.mode + trayMode
//   的字串拼接比較，任一個不同就當作有變化）——不能只比 seq，因為 recovering→idle 這類轉場不會
//   pushEvent、seq 不會前進，純 seq 節流會讓這個轉場永遠不反映到 React state，SkillTray 的
//   unavailableIds（讀 player.action）就會把技能/道具格鎖灰到下一次真的有戰鬥事件才解開
//   （2026-09-14 審查抓到的真的 bug，不是理論疑慮：最長可能卡到 4.5s）。沒有變化代表這一幀只是計時器
//   繼續累加，全部交給呼叫端（BattleScreen）自己的 rAF 用 liveStateRef 讀「這一刻最新但還沒進 React
//   state」的引擎狀態，自行決定要不要用更細的節流（例如冷卻秒數變了才 setState、蓄氣條每幀直接更新）。
//   ⚠️ 這個節流只套用在 tick() 迴圈，send() 一律立即 setState——SET_TRAY／CANCEL_TARGETING／
//   USE_SKILL・USE_ITEM 進入 chooseAlly 選隊友這幾種指令只改 trayMode/targeting 這類「UI 模式」欄位，
//   engine 判定它們不算「戰鬥事件」所以不會 pushEvent、seq 不會前進；若 send() 也套用節流，
//   這幾個指令在畫面上會完全沒有反應（實測踩過：點「道具」鈕切不到道具列）。使用者指令的呼叫頻率
//   遠低於 60fps，本來就不需要節流，詳見 commitEvents/send 的實作與註解。
import { useCallback, useEffect, useReducer, useRef } from 'react';
import type { BattleSample } from '@/lib/dorpg/types';
import {
  createBattle,
  dispatch as engineDispatch,
  drainEvents,
  tick as engineTick,
} from '@/lib/dorpg/engine';
import type { BattleConfig, BattleEvent, BattleState, Command } from '@/lib/dorpg/engine';

export type UseBattleOptions = {
  /** 覆寫 createBattle 的 config（僅初始化時使用；契約：除錯用高倍傷害/縮短血量走 sample/config 覆寫）。 */
  config?: Partial<BattleConfig>;
  /** 覆寫 rng（測試/除錯用；預設 Math.random，見 engine/index.ts）。 */
  rng?: () => number;
  /** 每次 dispatch/tick 產生新事件時呼叫（在 setState 之前同步呼叫，供 BattleScreen 觸發音效/特效/飄字）。 */
  onEvents?: (events: BattleEvent[], state: BattleState) => void;
  /**
   * 2026-09-19 修復：P9 玩家自動戰鬥開關的初始值（僅初始化時使用，同 config——見 createBattle
   * 的 opts.autoBattle）。本檔原本沒有把這個欄位轉交給 createBattle()，導致每次進戰鬥都無視
   * BattleScreenProps.autoBattle、永遠以 false 初始化引擎狀態。
   */
  autoBattle?: boolean;
};

export type UseBattleResult = {
  /** 「有意義變化」（seq 前進）才更新的 React state，用來驅動一般畫面渲染。 */
  state: BattleState;
  /** 送出玩家指令（內部自動先 tick 到目前時間，見 engine/dispatch.ts）。 */
  send: (cmd: Command) => void;
  /**
   * 讀取「這一刻最新」的引擎狀態（每幀 tick 都會更新，不受上面的 setState 節流影響）。
   * 用途：CommandBar 蓄氣條、攻擊/技能冷卻倒數這類需要逐幀讀數但不需要逐幀重繪整棵樹的顯示。
   * 只能讀，不能改——這是 ref，不是狀態來源；改變戰鬥狀態一律透過 send()。
   */
  liveStateRef: React.RefObject<BattleState>;
};

function reducer(_prev: BattleState, next: BattleState): BattleState {
  return next;
}

/**
 * tick() 迴圈判斷「要不要 setState」的依據——原本只比對 seq，但 recovering→idle 這類轉場
 * （攻擊/技能施放完的行為鎖解除）不會 pushEvent、seq 不會前進，純 seq 節流會讓這個轉場永遠不
 * 反映到 React state：畫面上技能/道具格會被 SkillTray 的 unavailableIds（讀 player.action !== 'idle'）
 * 鎖灰到「下一次真的有事件發生」為止，最長可能卡到 attackCooldownMs（1.5s）或某些技能 castMs+
 * recoveryMs 疊起來的秒數（2026-09-14 審查抓到：最長 4.5s）。
 * 修法：除了 seq，再比對幾個「會被 UI 直接讀、但 engine 不當作戰鬥事件」的欄位——玩家的
 * action（idle/charging/guarding/casting/recovering/dead，決定按鈕與技能格能不能按）、phase、
 * escape.flow、targeting.mode、trayMode。任一個字串不同就當作「有變化」，簡單用字串拼接比較
 * （這幾個欄位都是純量/淺層列舉值，字串比較足夠、不需要深比對整個 party/enemies 陣列）。
 */
function tickUiSignature(s: BattleState): string {
  const player = s.party.find((p) => p.isPlayer);
  return `${s.seq}|${player?.action ?? ''}|${s.phase}|${s.escape.flow}|${s.targeting.mode}|${s.trayMode}`;
}

export function useBattle(sample: BattleSample, opts: UseBattleOptions = {}): UseBattleResult {
  // opts 每次 render 都可能是新物件（呼叫端多半是行內定義），但這裡只需要「最新的那一份」，
  // 不需要因為它變動就重建整個 hook——用 ref 存、每次 render 都覆寫，commit() 內永遠讀得到最新版本。
  const optsRef = useRef(opts);
  optsRef.current = opts;

  // 只在第一次 render 建立戰鬥（sample 之後就算換了新 identity 也不重建——與大多數「初始化用的 props」
  // 慣例一致，戰鬥中途換隊伍/敵人陣容不是 P1 的需求）。
  const [state, localSet] = useReducer(reducer, undefined, () => {
    const initial = createBattle(sample, { now: performance.now(), rng: opts.rng, config: opts.config, autoBattle: opts.autoBattle });
    // createBattle 本身不產生事件，這裡仍過一次 drainEvents 確保回傳形狀（events:[]）跟後續一致。
    return drainEvents(initial).state;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  });

  const stateRef = useRef(state);
  stateRef.current = state;

  // 只負責「drain 事件＋回傳新 state」，不管要不要 setState——兩個呼叫端（send／tick 迴圈）
  // 對「要不要重繪」的節流需求不一樣，見下面兩處各自的說明。
  const commitEvents = useCallback((next: BattleState) => {
    const { events, state: drained } = drainEvents(next);
    if (events.length > 0) optsRef.current.onEvents?.(events, drained);
    return drained;
  }, []);

  const send = useCallback(
    (cmd: Command) => {
      // 使用者指令一律立即 setState，不套用 seq 節流：像 SET_TRAY／CANCEL_TARGETING／
      // USE_SKILL・USE_ITEM 進入 chooseAlly 選隊友這幾種轉場，engine 只是單純改 trayMode/targeting
      // 這類「UI 模式」欄位，語意上不算「戰鬥事件」所以不會 pushEvent、seq 不會前進——如果沿用跟
      // tick() 一樣「只在 seq 前進時才 setState」的節流，這幾個指令在畫面上會完全沒有反應
      // （點「道具」鈕切不到道具列、選技能選不出「選隊友」高亮——這是本檔實測踩過的一個真的 bug，
      // 不是理論疑慮）。使用者指令的呼叫頻率遠低於 60fps（人手按鈕），不需要、也不應該對它節流。
      const drained = commitEvents(engineDispatch(stateRef.current, cmd, performance.now()));
      stateRef.current = drained;
      localSet(drained);
    },
    [commitEvents],
  );

  // 主迴圈：只在 active/resolving 時跑（ended/loading 不必推進；resolving 目前 engine 未實際產生，
  // 保留判斷純粹對齊型別，不影響行為）。這裡才是真正需要節流的地方——每秒最多呼叫 60 次。
  useEffect(() => {
    if (state.phase !== 'active' && state.phase !== 'resolving') return;
    let raf = 0;
    const loop = () => {
      // 分頁在背景時完全跳過 tick：不產生新的計時推進，回前景後從目前 wall-clock 時間繼續
      // （見檔頭說明的已知邊界情況）。HOLD_CANCEL/GUARD_END 由下面另一個 effect 監聽 visibilitychange 送出。
      if (!document.hidden) {
        const drained = commitEvents(engineTick(stateRef.current, performance.now()));
        const prevSig = tickUiSignature(stateRef.current);
        // liveStateRef 永遠更新（給 BattleScreen 的逐幀讀數用），不論等一下要不要觸發 React 重繪。
        stateRef.current = drained;
        if (tickUiSignature(drained) !== prevSig) {
          // 不能只看 seq（見 tickUiSignature 檔頭說明：recovering→idle 這類轉場不會 pushEvent），
          // 這裡額外比對幾個 UI 會直接讀的欄位，任一個變了就重繪。
          localSet(drained);
        }
      }
      raf = requestAnimationFrame(loop);
    };
    raf = requestAnimationFrame(loop);
    return () => cancelAnimationFrame(raf);
  }, [state.phase, commitEvents]);

  // 契約 §7：document.hidden → 送 HOLD_CANCEL 與 GUARD_END（保護玩家目前按著的攻擊蓄力／防禦，
  // 避免切走後手指仍按著、回來才觸發攻擊或防禦卡住）；回前景不需要額外動作，上面的主迴圈會自然恢復推進。
  useEffect(() => {
    const onVisibility = () => {
      if (!document.hidden) return;
      const s = stateRef.current;
      const player = s.party.find((p) => p.id === s.playerId);
      if (!player) return;
      if (player.action === 'charging') send({ type: 'HOLD_CANCEL' });
      else if (player.action === 'guarding') send({ type: 'GUARD_END' });
    };
    document.addEventListener('visibilitychange', onVisibility);
    return () => document.removeEventListener('visibilitychange', onVisibility);
  }, [send]);

  return { state, send, liveStateRef: stateRef };
}
