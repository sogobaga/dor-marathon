'use client';

// DORPG P2：遭遇選單（全頁，PhoneShell 子頁慣例）。角色頁「進入戰鬥」的新入口——P1 是直接開
// BattleScreen 套範例資料，P2 起先來這裡選一場真實遭遇，PhoneShell 再依選到的 code 打 bootstrap
// 進戰鬥。資料自己打 API（跟 CharacterScreen.tsx 現有慣例一致：畫面元件自己 fetch，PhoneShell 只管
// 顯示/隱藏與導覽），但保留 loadOverride 給 /dev 預覽頁在沒有後端/DB 時注入 fixture 資料
// （見契約 §4／§7.4：/dev/dorpg 要能在「選單」這一步也走離線 fixture 模式）。
//
// 畫面固定 data-skin="default" 暗色（跟 BattleScreen/ResultOverlay 同一套 PALETTE），不是站方 skin
// 系統的可切換配色——這是 DORPG 這整包子系統自己的視覺語言,不是既有「事件面板」那種要固定亮字的情況。
import { useCallback, useEffect, useState } from 'react';
import { rpgApi, rpgBattleApi, type JobDTO, type RpgBattleEncounters, type RpgEncounterSummary } from '@/lib/api';
import { getUserToken, withUserAuth } from '@/lib/userAuth';
import { sortJobs } from '@/lib/rpgMeta';
import { PALETTE, kitAsset, nineSliceStyle } from '@/lib/dorpg/assets';
import { battleAudio } from '@/lib/dorpg/audio';
import styles from './EncounterPicker.module.css';

export type EncounterPickerProps = {
  onBack: () => void;
  onPick: (code: string) => void;
  /** 未配點提醒／角色摘要列點擊 → 回角色頁配點（契約 §4：擁有者現況 34 點未配是關鍵引導）。 */
  onOpenCharacter: () => void;
  /**
   * 測試/開發專用：注入資料來源取代真正的 rpgBattleApi.encounters()（見 app/dev/dorpg，僅
   * DORPG_DEV=1 才存在的離線 fixture 模式）。正式呼叫端（PhoneShell）不會傳這個，一律走真正的 API。
   */
  loadOverride?: () => Promise<RpgBattleEncounters>;
};

function formatBestTime(ms: number | null | undefined): string {
  if (!ms || ms <= 0) return '';
  const total = Math.round(ms / 1000);
  return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, '0')}`;
}

function difficultyStars(n: number): string {
  const clamped = Math.max(0, Math.min(5, Math.round(n)));
  return '★'.repeat(clamped) + '☆'.repeat(5 - clamped);
}

export default function EncounterPicker({ onBack, onPick, onOpenCharacter, loadOverride }: EncounterPickerProps) {
  const [data, setData] = useState<RpgBattleEncounters | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const load = useCallback(() => {
    setLoading(true);
    setError('');
    const run: Promise<RpgBattleEncounters> = loadOverride
      ? loadOverride()
      : getUserToken()
        ? withUserAuth((t) => rpgBattleApi.encounters(t))
        : Promise.reject({ status: 401 });
    run
      .then((r) => setData(r))
      .catch((e: any) => {
        // 401/403：入口理論上已經 gate 過（契約 §4），這裡只是保險；503＝migration 176 尚未套用，
        // 後端會帶清楚訊息在 e.message，直接顯示即可，不必自己重複一份文案。
        setError(e?.status === 401 || e?.status === 403 ? '尚未登入或沒有權限使用戰鬥功能' : e?.message || '載入失敗，請稍後再試');
      })
      .finally(() => setLoading(false));
  }, [loadOverride]);

  useEffect(() => {
    load();
  }, [load]);

  // DORPG P5：職業切換列（見契約 §1／WIRE §會員端 REST）。RpgBattleCharacterBrief（bootstrap 的
  // character 摘要）沒有帶 job 欄位——契約沒有把它加進那個 wire 型別，這裡改叫既有的 /rpg/jobs
  // + /rpg/me 取得清單與目前職業，不擅自幫 BACKEND 的 wire 型別加欄位。/dev 離線 fixture 模式
  // （loadOverride 有值）或未登入時不打真正 API，職業列直接不顯示（失敗也不擋主畫面）。
  const [jobs, setJobs] = useState<JobDTO[]>([]);
  const [currentJobId, setCurrentJobId] = useState<string | null>(null);
  const [jobBusy, setJobBusy] = useState(false);

  const loadJob = useCallback(() => {
    if (loadOverride || !getUserToken()) return;
    Promise.all([withUserAuth((t) => rpgApi.jobs(t)), withUserAuth((t) => rpgApi.me(t))])
      .then(([jobsRes, me]) => {
        setJobs(sortJobs(jobsRes.jobs));
        setCurrentJobId(me.character?.job?.id ?? null);
      })
      .catch(() => {});
  }, [loadOverride]);
  useEffect(() => {
    loadJob();
  }, [loadJob]);

  async function selectJob(id: string) {
    if (jobBusy || id === currentJobId) return;
    setJobBusy(true);
    try {
      const me = await withUserAuth((t) => rpgApi.setJob(t, id));
      setCurrentJobId(me.character?.job?.id ?? null);
      // 職業會決定 bootstrap 的武器視覺（契約 §1）；重新打一次既有的 encounters 摘要載入機制，
      // 之後玩家挑選遭遇打 bootstrap 時後端已經是新職業，會自動反映新武器，這裡不必自己重打 bootstrap。
      load();
    } catch {
      /* 職業切換列是加分功能，失敗不顯示錯誤橫幅、不擋主畫面（沿用 loadJob 的靜默失敗原則） */
    } finally {
      setJobBusy(false);
    }
  }

  const ch = data?.character;

  // 2026-09-14 SCREENS 接線：選單是整個戰鬥流程的入口，音樂應該從這裡就開始、跨越多場戰鬥連續播放
  // （見 audio.ts 檔頭修復記錄）。playBgmFromGesture 必須同步呼叫、前面不可有 await——第一行就是
  // handler 本體。固定播 'master'：進 boss 場遭遇後由 BattleScreen 自己的手勢入口切成 'boss'，
  // 同一顆 <audio> 元素接續播放，不會有斷音；playBgmFromGesture 對同一個 kind 重複呼叫是 no-op，
  // 玩家在選單多次點按不會重播/重設進度。
  // 2026-09-14 修復 C：同時掛在 pointerdown/pointerup/click 三種事件的 capture 階段——理由與
  // BattleScreen.tsx 同一處註解一致：WebKit 對合法使用者手勢的認定比 Chromium 嚴格，只掛
  // pointerdown 一種若剛好不被 iOS 認可，會讓每次重試都卡在同一個被拒模式裡。
  const handleRootPointerDownCapture = () => {
    battleAudio.playBgmFromGesture('master');
  };

  return (
    <div
      className={styles.root}
      data-skin="default"
      style={{ background: PALETTE.surfaceBase, color: PALETTE.textPrimary }}
      onPointerDownCapture={handleRootPointerDownCapture}
      onPointerUpCapture={handleRootPointerDownCapture}
      onClickCapture={handleRootPointerDownCapture}
    >
      <header className={styles.header}>
        <button type="button" className={styles.backBtn} style={{ color: PALETTE.textSecondary }} onClick={onBack}>
          ← 返回
        </button>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img className={styles.logo} src={kitAsset('logo_dorpg')} alt="DORPG" draggable={false} />
        <span className={styles.headerSpacer} aria-hidden="true" />
      </header>

      <div className={styles.scroll}>
        <div className={styles.inner}>
          {loading && <PickerSkeleton />}

          {!loading && error && (
            <div className={styles.center}>
              <p className={styles.errorText} style={{ color: PALETTE.textSecondary }}>
                {error}
              </p>
              <button type="button" className={styles.retryBtn} style={{ borderColor: PALETTE.borderGold, color: PALETTE.textPrimary }} onClick={load}>
                重試
              </button>
            </div>
          )}

          {!loading && !error && data && (
            <>
              {jobs.length > 0 && (
                <div className={styles.jobRow} role="group" aria-label="切換職業">
                  {jobs.map((j) => {
                    const active = j.id === currentJobId;
                    return (
                      <button
                        key={j.id}
                        type="button"
                        className={active ? `${styles.jobPill} ${styles.jobPillActive}` : styles.jobPill}
                        style={active ? { background: PALETTE.borderGold, borderColor: PALETTE.borderGold, color: '#fff' } : { borderColor: 'rgba(243,189,98,.3)', color: PALETTE.textSecondary }}
                        disabled={jobBusy}
                        onClick={() => selectJob(j.id)}
                      >
                        {j.name}
                      </button>
                    );
                  })}
                </div>
              )}

              {ch && (
                <button
                  type="button"
                  className={styles.charCard}
                  style={nineSliceStyle('frame_dialog')}
                  onClick={onOpenCharacter}
                  aria-label={`角色摘要，等級 ${ch.base_level}，戰力 ${ch.power}${ch.free_points > 0 ? `，還有 ${ch.free_points} 點未配置` : ''}，點擊前往角色頁`}
                >
                  <div className={styles.charTop}>
                    <span className={styles.charLv} style={{ color: PALETTE.borderGold }}>
                      Lv.{ch.base_level}
                    </span>
                    <span className={styles.chip}>
                      HP <b>{ch.max_hp}</b>
                    </span>
                    <span className={styles.chip}>
                      MP <b>{ch.max_mp}</b>
                    </span>
                    <span className={styles.chip}>
                      戰力 <b>{ch.power}</b>
                    </span>
                  </div>
                  {ch.free_points > 0 && (
                    <div className={styles.hint} style={{ color: PALETTE.targetGold }}>
                      你還有 {ch.free_points} 點未配置 →
                    </div>
                  )}
                </button>
              )}

              {data.encounters.length === 0 ? (
                <div className={styles.center}>
                  <p className={styles.errorText} style={{ color: PALETTE.textSecondary }}>目前沒有可挑戰的遭遇</p>
                </div>
              ) : (
                <div className={styles.list}>
                  {data.encounters.map((enc) => (
                    <EncounterCard key={enc.code} enc={enc} onPick={() => onPick(enc.code)} />
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function EncounterCard({ enc, onPick }: { enc: RpgEncounterSummary; onPick: () => void }) {
  const best = formatBestTime(enc.stats.best_ms);
  const recordText = enc.stats.plays > 0 ? `${enc.stats.plays} 戰 ${enc.stats.wins} 勝${best ? ` · 最快 ${best}` : ''}` : '尚未挑戰';

  return (
    <button type="button" className={styles.card} style={{ borderColor: 'rgba(243,189,98,.28)', background: PALETTE.surfacePanel }} onClick={onPick}>
      <div className={styles.thumbWrap}>
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img className={styles.thumb} src={enc.scene_image_url} alt="" draggable={false} />
        {enc.scene_kind === 'boss' && (
          <span className={styles.bossTag} style={{ background: PALETTE.criticalGlow }}>
            BOSS
          </span>
        )}
      </div>
      <div className={styles.cardBody}>
        <div className={styles.cardTitleRow}>
          <span className={styles.cardTitle}>{enc.title}</span>
          <span className={styles.stars} style={{ color: PALETTE.borderGold }} aria-label={`難度 ${enc.difficulty} 星`}>
            {difficultyStars(enc.difficulty)}
          </span>
        </div>
        {enc.subtitle ? (
          <div className={styles.cardSubtitle} style={{ color: PALETTE.textSecondary }}>
            {enc.subtitle}
          </div>
        ) : null}
        {/* DORPG P6（契約 §2）：怪物等級制——每場一個固定 monster_level，六場依序 10/20/30/40/50/60，
            讓玩家事先知道這場的強度感覺，不必打進去才發現太硬/太弱。 */}
        <div className={styles.cardSubtitle} style={{ color: PALETTE.borderGold, fontWeight: 700 }}>
          怪物 Lv.{Math.floor(enc.monster_level)}
        </div>
        <div className={styles.monsterRow}>
          {enc.monsters.map((m, i) => (
            // eslint-disable-next-line @next/next/no-img-element
            <img key={`${m.slot}-${i}`} className={m.is_boss ? `${styles.monsterThumb} ${styles.monsterBoss}` : styles.monsterThumb} src={m.poster_url} alt={m.name} draggable={false} />
          ))}
        </div>
        <div className={styles.record} style={{ color: PALETTE.textSecondary }}>
          {recordText}
        </div>
      </div>
    </button>
  );
}

function PickerSkeleton() {
  return (
    <div className={styles.skeletonList} aria-hidden="true">
      {[0, 1, 2].map((i) => (
        <div key={i} className={styles.skeletonCard} />
      ))}
    </div>
  );
}
