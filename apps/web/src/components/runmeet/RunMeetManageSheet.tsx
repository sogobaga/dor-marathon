'use client'

import { useState } from 'react'
import useSWR from 'swr'
import { runMeetApi, type RunMeetMemberDetail, type RunMeetMember } from '@/lib/api'
import { withUserAuth } from '@/lib/userAuth'
import { Avatar, RunMeetModal, dangerBtn, errText, fieldHint, ghostBtn, modalTitle, primaryBtn, tinyBtn } from './ui'
import { memberCountText } from '@/lib/runMeet'

// 發起人管理面板：審核專區（同意/婉拒/全部同意）、成員名單（剔除）、生命週期（編輯/關閉/取消/刪除）。
//
// ⚠️ 名額競態全在後端（run_meets FOR UPDATE + CAS 核銷申請）：前端只負責顯示 per-item 結果，
//    「已同意 3 人，2 人因名額已滿未處理」這種回饋直接來自 approve-batch 的 results 陣列。
// ⚠️ 被剔除者沒有列表端點（成員清單只支援 joined/pending），所以「解除封鎖」只在本次面板內
//    針對「剛剛移出的人」提供撤銷；離開面板後要解除需請對方聯繫發起人（後端 unban 端點仍在）。
export default function RunMeetManageSheet({ meet, onClose, onEdit, onChanged, onToast }: {
  meet: RunMeetMemberDetail
  onClose: () => void
  onEdit: () => void
  onChanged: () => void
  onToast: (text: string, tone?: 'ok' | 'err') => void
}) {
  const [busy, setBusy] = useState('')
  const [err, setErr] = useState('')
  const [confirmKick, setConfirmKick] = useState<RunMeetMember | null>(null)
  // 'open'/'unhide' 不進這裡——那兩個是正向、可逆的動作，直接執行不需要確認彈窗。
  const [confirmAct, setConfirmAct] = useState<'close' | 'hide' | 'cancel' | 'delete' | null>(null)
  const [justKicked, setJustKicked] = useState<RunMeetMember[]>([])

  const { data: joinedData, mutate: reloadJoined } = useSWR(
    ['run-meet-members', meet.id, 'joined'],
    () => withUserAuth((t) => runMeetApi.members(t, meet.id, 'joined')),
  )
  const { data: pendingData, mutate: reloadPending } = useSWR(
    ['run-meet-members', meet.id, 'pending'],
    () => withUserAuth((t) => runMeetApi.members(t, meet.id, 'pending')),
  )
  const joined = joinedData?.items ?? []
  const pending = pendingData?.items ?? []

  function refreshAll() { void reloadJoined(); void reloadPending(); onChanged() }

  // okMsg 可以是函式：中止團練要依回應的 rejected 筆數顯示不同文案。
  async function run(key: string, fn: () => Promise<any>, okMsg: string | ((res: any) => string)) {
    if (busy) return
    setBusy(key); setErr('')
    try {
      const res = await fn()
      refreshAll()
      onToast(typeof okMsg === 'function' ? okMsg(res) : okMsg)
    } catch (e: any) {
      setErr(e?.message || '系統忙碌中，請稍後再試')
    } finally {
      setBusy('')
    }
  }

  async function approveAll() {
    if (busy || pending.length === 0) return
    setBusy('batch'); setErr('')
    try {
      const res = await withUserAuth((t) => runMeetApi.approveBatch(t, meet.id, pending.slice(0, 50).map((p) => p.user_id)))
      refreshAll()
      onToast(res.failed > 0 ? `已同意 ${res.approved} 人，${res.failed} 人未處理` : `已同意 ${res.approved} 人`, res.failed > 0 ? 'err' : 'ok')
      if (res.failed > 0) {
        const first = res.results.find((r) => !r.ok && r.error)
        if (first?.error) setErr(first.error)
      }
    } catch (e: any) {
      setErr(e?.message || '系統忙碌中，請稍後再試')
    } finally { setBusy('') }
  }

  const ended = meet.is_ended
  // closed/cancelled 期間「同意」＝收人，後端一律擋（見 lib/runMeet.ts 對 status 的說明）。
  const approvalBlocked = meet.status === 'closed' || meet.status === 'cancelled'
  return (
    <>
      <RunMeetModal onClose={onClose} maxWidth={392}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <div style={{ ...modalTitle, flex: 1, textAlign: 'left' }}>管理團練</div>
          <button type="button" onClick={onClose} style={{ ...ghostBtn, padding: '5px 10px' }}>關閉</button>
        </div>
        <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 6, lineHeight: 1.6 }}>
          {meet.title}｜{memberCountText(meet.member_count, meet.capacity)}
        </div>

        {err && <div style={errText}>{err}</div>}

        {/* 審核專區 */}
        <SectionTitle text={`審核專區（${pending.length}）`} />
        {pending.length === 0 ? (
          <div style={{ fontSize: 12.5, color: 'var(--tx-faint)', padding: '4px 0 2px' }}>目前沒有待審核的申請</div>
        ) : (
          <>
            {/* closed（暫停收人）/ cancelled（中止）期間後端一律擋「同意」（同意＝收人）；
                婉拒不影響名額，仍可進行。提前擋掉按鈕，避免使用者按了才吃 409。 */}
            {approvalBlocked && (
              <div style={{ ...fieldHint, marginTop: 0, marginBottom: 8 }}>
                目前{meet.status === 'closed' ? '已關閉' : '已中止'}，暫時無法同意新的加入申請；婉拒不受影響。
              </div>
            )}
            <button type="button" onClick={approveAll} disabled={!!busy || approvalBlocked} style={{ ...primaryBtn, padding: '9px 0', fontSize: 13, marginBottom: 8, opacity: approvalBlocked ? 0.5 : 1 }}>
              {busy === 'batch' ? '處理中…' : `全部同意（${Math.min(pending.length, 50)}）`}
            </button>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {pending.map((p) => (
                <div key={p.user_id} style={{ display: 'flex', alignItems: 'center', gap: 8, background: 'var(--bg-2)', borderRadius: 10, padding: '8px 10px' }}>
                  <Avatar url={p.avatar_url} name={p.name} size={28} />
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontSize: 13, fontWeight: 800, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{p.name}</div>
                    {p.apply_note && <div style={{ fontSize: 11.5, color: 'var(--tx-dim)', wordBreak: 'break-word' }}>{p.apply_note}</div>}
                  </div>
                  <button type="button" disabled={!!busy || approvalBlocked} style={{ ...tinyBtn, opacity: approvalBlocked ? 0.5 : 1 }} onClick={() => run(`ap-${p.user_id}`, () => withUserAuth((t) => runMeetApi.approve(t, meet.id, p.user_id)), `已讓「${p.name}」加入`)}>同意</button>
                  <button type="button" disabled={!!busy} onClick={() => run(`rj-${p.user_id}`, () => withUserAuth((t) => runMeetApi.reject(t, meet.id, p.user_id)), `已婉拒「${p.name}」的申請`)} style={{ ...tinyBtn, color: 'var(--hunt)' }}>婉拒</button>
                </div>
              ))}
            </div>
          </>
        )}

        {/* 成員名單 */}
        <SectionTitle text={`成員（${joined.length}）`} />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {joined.map((m) => (
            <div key={m.user_id} style={{ display: 'flex', alignItems: 'center', gap: 8, background: 'var(--bg-2)', borderRadius: 10, padding: '8px 10px' }}>
              <Avatar url={m.avatar_url} name={m.name} size={28} />
              <div style={{ flex: 1, minWidth: 0, fontSize: 13, fontWeight: 800, color: 'var(--tx)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                {m.name}{m.is_owner && <span style={{ color: 'var(--gold)', fontSize: 11, marginLeft: 6 }}>發起人</span>}
              </div>
              {!m.is_owner && <button type="button" disabled={!!busy} onClick={() => setConfirmKick(m)} style={{ ...tinyBtn, color: 'var(--hunt)' }}>移出</button>}
            </div>
          ))}
          {joined.length <= 1 && (
            <div style={{ fontSize: 12.5, color: 'var(--tx-faint)', lineHeight: 1.7 }}>還沒有其他人加入。把團練分享出去，找人一起跑！</div>
          )}
        </div>

        {justKicked.length > 0 && (
          <>
            <SectionTitle text="剛移出的成員" />
            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
              {justKicked.map((m) => (
                <div key={m.user_id} style={{ display: 'flex', alignItems: 'center', gap: 8, background: 'var(--bg-2)', borderRadius: 10, padding: '8px 10px' }}>
                  <Avatar url={m.avatar_url} name={m.name} size={28} />
                  <div style={{ flex: 1, minWidth: 0, fontSize: 13, color: 'var(--tx-dim)' }}>{m.name}</div>
                  <button
                    type="button" disabled={!!busy} style={tinyBtn}
                    onClick={() => run(`ub-${m.user_id}`, async () => {
                      await withUserAuth((t) => runMeetApi.unban(t, meet.id, m.user_id))
                      setJustKicked((arr) => arr.filter((x) => x.user_id !== m.user_id))
                    }, `已解除「${m.name}」的加入限制`)}
                  >解除</button>
                </div>
              ))}
            </div>
            <div style={fieldHint}>解除後對方可重新申請/加入（不會自動加回）。</div>
          </>
        )}

        {/* 生命週期 */}
        <SectionTitle text="團練設定" />
        <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
          {/* 已過期的團後端一律擋編輯（一次配額不該能無限回收成「新團練」，見 repository.UpdateMeet），
              所以這裡也不給按鈕，避免按了才吃 409。 */}
          {!ended
            ? <button type="button" onClick={onEdit} style={{ ...ghostBtn, width: '100%' }}>✎ 編輯團練資料</button>
            : <div style={fieldHint}>這個團練的時間已經過了，無法再編輯；要再揪一次請重新發起。</div>}

          {/* 1) 開放／關閉切換：closed＝暫停收新成員，其他功能（編輯、留言、成員）都照舊；
              cancelled 狀態不在這裡出現「關閉」——cancelled→closed 後端不合法（409），
              從 cancelled 只能重新開啟，見下面第 3 個動作。 */}
          {!ended && meet.status === 'open' && (
            <button type="button" onClick={() => setConfirmAct('close')} style={{ ...ghostBtn, width: '100%' }}>🚫 關閉團練（不再收新成員）</button>
          )}
          {!ended && meet.status === 'closed' && (
            <button
              type="button" disabled={!!busy} style={{ ...ghostBtn, width: '100%', opacity: busy ? 0.7 : 1 }}
              onClick={() => void run('open', () => withUserAuth((t) => runMeetApi.setStatus(t, meet.id, 'open')), '團練已重新開啟')}
            >✅ 重新開啟團練</button>
          )}

          {/* 2) 隱藏／取消隱藏：只影響探索/連結曝光，不影響 status，發起人與現有成員照常查看與留言。
              取消隱藏是正向、可逆動作，不需要確認彈窗；隱藏會改變其他人能不能找到這個團練，需要確認。 */}
          {meet.hidden_by_owner ? (
            <button
              type="button" disabled={!!busy} style={{ ...ghostBtn, width: '100%', opacity: busy ? 0.7 : 1 }}
              onClick={() => void run('unhide', () => withUserAuth((t) => runMeetApi.setVisibility(t, meet.id, false)), '已取消隱藏')}
            >👁 取消隱藏</button>
          ) : (
            <button type="button" onClick={() => setConfirmAct('hide')} style={{ ...ghostBtn, width: '100%' }}>🙈 隱藏團練</button>
          )}

          {/* 3) 中止／重新開啟：已中止時把危險鈕換成「重新開啟團練」（cancelled→open 是唯一合法轉換）。 */}
          {meet.status === 'cancelled' ? (
            <button
              type="button" disabled={!!busy} style={{ ...ghostBtn, width: '100%', opacity: busy ? 0.7 : 1 }}
              onClick={() => void run('open', () => withUserAuth((t) => runMeetApi.setStatus(t, meet.id, 'open')), '團練已重新開啟')}
            >✅ 重新開啟團練</button>
          ) : (
            <button type="button" onClick={() => setConfirmAct('cancel')} style={{ ...dangerBtn, width: '100%' }}>✕ 中止團練</button>
          )}

          {/* 4) 刪除：不可復原 */}
          <button type="button" onClick={() => setConfirmAct('delete')} style={{ ...dangerBtn, width: '100%' }}>🗑 刪除團練</button>
          <div style={fieldHint}>關閉、中止或刪除都<b>不會返還</b>本月的發起次數。</div>
        </div>
      </RunMeetModal>

      {confirmKick && (
        <RunMeetModal onClose={() => setConfirmKick(null)} maxWidth={330}>
          <div style={modalTitle}>移出成員</div>
          <div style={{ fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, marginTop: 12 }}>
            要把「{confirmKick.name}」移出團練嗎？移出後對方將無法再次加入（可在本次管理面板中解除）。
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 18 }}>
            <button type="button" onClick={() => setConfirmKick(null)} style={{ ...ghostBtn, width: '100%', padding: '11px 0' }}>取消</button>
            <button
              type="button" style={{ ...primaryBtn, background: 'var(--hunt)', color: '#fff' }}
              onClick={() => {
                const target = confirmKick
                setConfirmKick(null)
                void run(`kick-${target.user_id}`, async () => {
                  await withUserAuth((t) => runMeetApi.kick(t, meet.id, target.user_id))
                  setJustKicked((arr) => (arr.some((x) => x.user_id === target.user_id) ? arr : [...arr, target]))
                }, `已將「${target.name}」移出團練`)
              }}
            >確定移出</button>
          </div>
        </RunMeetModal>
      )}

      {confirmAct && (
        <RunMeetModal onClose={() => setConfirmAct(null)} maxWidth={340}>
          <div style={modalTitle}>
            {confirmAct === 'close' ? '關閉團練' : confirmAct === 'hide' ? '隱藏團練' : confirmAct === 'cancel' ? '中止團練' : '刪除團練'}
          </div>
          <div style={{ fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, marginTop: 12 }}>
            {confirmAct === 'close' && <>關閉只是不再收新成員，現有成員、留言與內容都照舊，待審申請會保留，重新開啟後可繼續處理。<b>發起次數不會返還。</b></>}
            {confirmAct === 'hide' && <>隱藏後不會出現在團練探索，連結也不再開放給其他人；發起人與已加入的成員仍可正常查看與留言。</>}
            {confirmAct === 'cancel' && <>中止後將停止一切加入動作，想加入的人會看到「該團練已中止」；所有待審申請會一併婉拒。之後仍可重新開啟。<b>發起次數不會返還。</b></>}
            {confirmAct === 'delete' && <>刪除後這個團練會從探索與所有人的列表消失，透過連結進入的人也會看到已刪除的提示，<b>無法復原</b>。<b>發起次數不會返還。</b></>}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 18 }}>
            <button type="button" onClick={() => setConfirmAct(null)} style={{ ...ghostBtn, width: '100%', padding: '11px 0' }}>返回</button>
            <button
              type="button" style={{ ...primaryBtn, background: 'var(--hunt)', color: '#fff' }}
              onClick={() => {
                const act = confirmAct
                setConfirmAct(null)
                if (act === 'close') void run('close', () => withUserAuth((t) => runMeetApi.setStatus(t, meet.id, 'close')), '團練已關閉')
                else if (act === 'hide') void run('hide', () => withUserAuth((t) => runMeetApi.setVisibility(t, meet.id, true)), '已隱藏團練')
                else if (act === 'cancel') void run('cancel', () => withUserAuth((t) => runMeetApi.setStatus(t, meet.id, 'cancel')), (res) => (res?.rejected > 0 ? `已婉拒 ${res.rejected} 筆待審申請` : '團練已中止'))
                else void run('delete', async () => { await withUserAuth((t) => runMeetApi.remove(t, meet.id)); onClose() }, '團練已刪除')
              }}
            >確定</button>
          </div>
        </RunMeetModal>
      )}
    </>
  )
}

function SectionTitle({ text }: { text: string }) {
  return <div style={{ fontSize: 12.5, fontWeight: 900, color: 'var(--tx)', margin: '18px 0 8px', paddingTop: 12, borderTop: '1px solid var(--line)' }}>{text}</div>
}
