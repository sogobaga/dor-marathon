'use client';

// DORPG 長按手勢 hook（P1）：移植自 07_DORPG_UI_Kit_v1/preview/dorpg-ui.js 的 bindHold，
// 原版直接在 DOM 元素上 addEventListener；這裡改成回傳一組可展開到 <button> 的 React props，
// 讓呼叫端（CommandBar 等）不必再自己接手勢邏輯，只要 {...useHoldGesture(...)} 展開即可。
// 規則照 使用與接入說明.md：pointer capture、滑出取消、鍵盤 Space/Enter、
// 視窗失焦／頁面切到背景一律視為取消、卸載清乾淨所有監聽。
//
// 「滑出取消」實測踩坑：Pointer Events 規格與 Chromium 實際行為都是——一旦 setPointerCapture 生效，
// pointerover/out/enter/leave 這些「boundary events」在該指標仍被捕獲期間完全不會派發（用 Playwright
// 對真的 Chromium 拖出鈕外驗證過：pointermove 一路收到到很遠處，pointerleave 從頭到尾沒有觸發一次）。
// 所以拖出取消不能只靠 onPointerLeave，必須跟原始 kit 的 bindHold 一樣，在 onPointerMove 裡自己用
// getBoundingClientRect() + 12px 容錯比對游標位置。onPointerLeave 仍保留、一樣會觸發 cancel，
// 當作極端情況（例如未來瀏覽器行為改變、或非主要按鍵路徑）的備援，兩者都指向同一個 finish('cancel')。
import { useCallback, useEffect, useRef } from 'react';
import type { KeyboardEvent as ReactKeyboardEvent, PointerEvent as ReactPointerEvent } from 'react';

export type HoldGestureHandlers = {
  /** 手勢成立（pointerdown 主鍵或鍵盤 Space/Enter）時呼叫一次。 */
  start(): void;
  /** 正常放開：heldMs 為 start 到放開之間的毫秒數，僅供 UI 估算蓄氣倍率，不是戰鬥權威時間。 */
  release(info: { heldMs: number }): void;
  /** 拖出鈕外／失焦／頁面隱藏／pointercancel／lostpointercapture：一律取消，不得誤觸原本的動作。 */
  cancel(): void;
};

export type HoldGestureOptions = {
  /** true 時完全 no-op：不呼叫 start/release/cancel，也不佔用 pointer capture。 */
  disabled?: boolean;
  /** 是否接受鍵盤 Space/Enter 觸發（預設 true，同 kit bindHold）。 */
  keyboard?: boolean;
};

export type HoldGestureProps = {
  onPointerDown: (e: ReactPointerEvent<HTMLButtonElement>) => void;
  onPointerUp: (e: ReactPointerEvent<HTMLButtonElement>) => void;
  onPointerCancel: (e: ReactPointerEvent<HTMLButtonElement>) => void;
  onPointerLeave: (e: ReactPointerEvent<HTMLButtonElement>) => void;
  /** 拖出取消的真正判定來源（見檔頭「滑出取消」實測踩坑）：pointer capture 期間量游標位置，
   * 超出鈕的邊框 + 12px 容錯即視同拖出。 */
  onPointerMove: (e: ReactPointerEvent<HTMLButtonElement>) => void;
  onKeyDown: (e: ReactKeyboardEvent<HTMLButtonElement>) => void;
  onKeyUp: (e: ReactKeyboardEvent<HTMLButtonElement>) => void;
  onLostPointerCapture: (e: ReactPointerEvent<HTMLButtonElement>) => void;
};

/** 拖出鈕外的容錯範圍（CSS px），逐字沿用 kit bindHold 的 12。 */
const LEAVE_SLOP_PX = 12;

export function useHoldGesture(handlers: HoldGestureHandlers, opts: HoldGestureOptions = {}): HoldGestureProps {
  const { disabled = false, keyboard = true } = opts;

  // 用 ref 存最新的 handlers/opts：呼叫端常常每次渲染都傳新的閉包（inline 函式），
  // 若直接放進 useCallback 依賴陣列，回傳的 props 物件會一直變動、造成按鈕不必要的重繫結。
  const handlersRef = useRef(handlers);
  handlersRef.current = handlers;
  const disabledRef = useRef(disabled);
  disabledRef.current = disabled;

  // pointer/key 只認一種輸入來源同時進行中（同 kit：pointer!==null||key!==null 即拒絕新的 start）；
  // 用 ref 而非 state，因為手勢期間本身不需要觸發重渲染，只有 start/release/cancel 這三個時機點才對外通知。
  const pointerIdRef = useRef<number | null>(null);
  const keyRef = useRef<string | null>(null);
  const startedAtRef = useRef(0);

  const start = useCallback((): boolean => {
    if (disabledRef.current || pointerIdRef.current !== null || keyRef.current !== null) return false;
    startedAtRef.current = performance.now();
    handlersRef.current.start();
    return true;
  }, []);

  // 統一收尾：release 與 cancel 共用「算 heldMs、清狀態」的邏輯，只有最後呼叫哪個回呼不同（照 kit 的 done()）。
  const finish = useCallback((kind: 'release' | 'cancel') => {
    if (pointerIdRef.current === null && keyRef.current === null) return;
    const heldMs = performance.now() - startedAtRef.current;
    pointerIdRef.current = null;
    keyRef.current = null;
    if (kind === 'release') handlersRef.current.release({ heldMs });
    else handlersRef.current.cancel();
  }, []);

  // 視窗失焦（切視窗／切 App）與頁面隱藏（切分頁、鎖螢幕）：不論目前是哪種輸入來源，一律取消，
  // 避免使用者切走時手指/滑鼠仍按著、回來後才觸發攻擊（kit bindHold 的 global blur / visibilitychange 規則）。
  useEffect(() => {
    const onWindowBlur = () => finish('cancel');
    const onVisibility = () => {
      if (document.hidden) finish('cancel');
    };
    window.addEventListener('blur', onWindowBlur);
    document.addEventListener('visibilitychange', onVisibility);
    return () => {
      window.removeEventListener('blur', onWindowBlur);
      document.removeEventListener('visibilitychange', onVisibility);
    };
  }, [finish]);

  // 元件卸載時若手勢仍在進行中，視同取消——卸載後才补送 release 會讓呼叫端對著已經不存在的按鈕結算傷害。
  useEffect(() => () => finish('cancel'), [finish]);

  function releasePointerCaptureSafely(e: ReactPointerEvent<HTMLButtonElement>) {
    try {
      if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
    } catch {
      // 部分瀏覽器對已釋放/不存在的 pointerId 呼叫 release 會丟例外，吞掉即可（kit bindHold 同樣用 try/catch）。
    }
  }

  const onPointerDown = useCallback(
    (e: ReactPointerEvent<HTMLButtonElement>) => {
      if (disabledRef.current) return;
      if (e.button !== 0) return; // 只認滑鼠左鍵；觸控／筆的主要按鍵 button 同樣是 0，不受影響。
      e.preventDefault();
      if (start()) {
        pointerIdRef.current = e.pointerId;
        e.currentTarget.setPointerCapture(e.pointerId);
      }
    },
    [start],
  );

  const onPointerUp = useCallback(
    (e: ReactPointerEvent<HTMLButtonElement>) => {
      if (pointerIdRef.current !== e.pointerId) return;
      releasePointerCaptureSafely(e);
      finish('release');
    },
    [finish],
  );

  const onPointerCancel = useCallback(
    (e: ReactPointerEvent<HTMLButtonElement>) => {
      if (pointerIdRef.current !== e.pointerId) return;
      releasePointerCaptureSafely(e);
      finish('cancel');
    },
    [finish],
  );

  // 備援：見檔頭說明，pointer capture 期間 pointerleave 實測不會觸發，真正的拖出判定在 onPointerMove。
  // 保留這個 handler 是為了型別/契約上仍然「有接」，且萬一某些輸入路徑真的觸發了 leave，一樣安全地取消。
  const onPointerLeave = useCallback(
    (e: ReactPointerEvent<HTMLButtonElement>) => {
      if (pointerIdRef.current !== e.pointerId) return;
      releasePointerCaptureSafely(e);
      finish('cancel');
    },
    [finish],
  );

  // 拖出鈕外的真正判定：比照 kit bindHold 的 move()，用目前按鈕的 getBoundingClientRect() 加 12px
  // 容錯，游標超出這個框就視同拖出、取消。用 currentTarget 量測（跟事件目標一致，即使按鈕本身有 CSS
  // transform／被 .phone-shell 縮放也是量它渲染後的實際框，不必另外換算座標）。
  const onPointerMove = useCallback(
    (e: ReactPointerEvent<HTMLButtonElement>) => {
      if (pointerIdRef.current !== e.pointerId) return;
      const r = e.currentTarget.getBoundingClientRect();
      const outside =
        e.clientX < r.left - LEAVE_SLOP_PX ||
        e.clientX > r.right + LEAVE_SLOP_PX ||
        e.clientY < r.top - LEAVE_SLOP_PX ||
        e.clientY > r.bottom + LEAVE_SLOP_PX;
      if (outside) {
        releasePointerCaptureSafely(e);
        finish('cancel');
      }
    },
    [finish],
  );

  const onLostPointerCapture = useCallback(
    (e: ReactPointerEvent<HTMLButtonElement>) => {
      if (pointerIdRef.current !== e.pointerId) return;
      finish('cancel');
    },
    [finish],
  );

  const onKeyDown = useCallback(
    (e: ReactKeyboardEvent<HTMLButtonElement>) => {
      if (disabledRef.current || !keyboard) return;
      if ((e.key === ' ' || e.key === 'Enter') && !e.repeat) {
        e.preventDefault();
        if (start()) keyRef.current = e.key;
      }
    },
    [keyboard, start],
  );

  const onKeyUp = useCallback(
    (e: ReactKeyboardEvent<HTMLButtonElement>) => {
      if (e.key !== keyRef.current) return;
      e.preventDefault();
      finish('release');
    },
    [finish],
  );

  return {
    onPointerDown,
    onPointerUp,
    onPointerCancel,
    onPointerLeave,
    onPointerMove,
    onKeyDown,
    onKeyUp,
    onLostPointerCapture,
  };
}
