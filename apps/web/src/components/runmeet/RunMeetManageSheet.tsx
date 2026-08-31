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
  const [confirmAct, setConfirmAct] = useState<'close' | 'cancel' | 'delete' | null>(null)
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

  async function run(key: string, fn: () => Promise<unknown>, okMsg: string) {
    if (busy) return
    setBusy(key); setErr('')
    try {
      await fn()
      refreshAll()
      onToast(okMsg)
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
            <button type="button" onClick={approveAll} disabled={!!busy} style={{ ...primaryBtn, padding: '9px 0', fontSize: 13, marginBottom: 8 }}>
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
                  <button type="button" disabled={!!busy} onClick={() => run(`ap-${p.user_id}`, () => withUserAuth((t) => runMeetApi.approve(t, meet.id, p.user_id)), `已讓「${p.name}」加入`)} style={tinyBtn}>同意</button>
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
          {meet.status === 'open' && !ended && (
            <button type="button" onClick={() => setConfirmAct('close')} style={{ ...ghostBtn, width: '100%' }}>🚫 關閉團練（不再收新成員）</button>
          )}
          {meet.status !== 'cancelled' && (
            <button type="button" onClick={() => setConfirmAct('cancel')} style={{ ...dangerBtn, width: '100%' }}>✕ 取消團練</button>
          )}
          <button type="button" onClick={() => setConfirmAct('delete')} style={{ ...dangerBtn, width: '100%' }}>🗑 刪除團練</button>
          <div style={fieldHint}>關閉、取消或刪除都<b>不會返還</b>本月的發起次數。</div>
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
            {confirmAct === 'close' ? '關閉團練' : confirmAct === 'cancel' ? '取消團練' : '刪除團練'}
          </div>
          <div style={{ fontSize: 13.5, color: 'var(--tx)', lineHeight: 1.8, marginTop: 12 }}>
            {confirmAct === 'close' && <>關閉後將不再收新成員，現有團員仍可留言互動。待審核的申請會一併婉拒。<b>發起次數不會返還。</b></>}
            {confirmAct === 'cancel' && <>取消後會通知所有團員，且無法復原。<b>發起次數不會返還。</b></>}
            {confirmAct === 'delete' && <>刪除後這個團練會從探索與所有人的列表消失，無法復原。<b>發起次數不會返還。</b></>}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 10, marginTop: 18 }}>
            <button type="button" onClick={() => setConfirmAct(null)} style={{ ...ghostBtn, width: '100%', padding: '11px 0' }}>返回</button>
            <button
              type="button" style={{ ...primaryBtn, background: 'var(--hunt)', color: '#fff' }}
              onClick={() => {
                const act = confirmAct
                setConfirmAct(null)
                if (act === 'close') void run('close', () => withUserAuth((t) => runMeetApi.close(t, meet.id)), '團練已關閉')
                else if (act === 'cancel') void run('cancel', () => withUserAuth((t) => runMeetApi.cancel(t, meet.id)), '團練已取消')
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
