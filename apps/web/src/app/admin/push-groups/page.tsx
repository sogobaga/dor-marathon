'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminPushGroupsApi, type PushGroup, type PushGroupDetail } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'

export default function AdminPushGroupsPage() {
  const router = useRouter()
  const [token, setTok] = useState<string | null>(null)
  const [groups, setGroups] = useState<PushGroup[] | null>(null)
  const [err, setErr] = useState('')

  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [detail, setDetail] = useState<PushGroupDetail | null>(null)
  const [detailErr, setDetailErr] = useState('')

  const [newName, setNewName] = useState('')
  const [creating, setCreating] = useState(false)
  const [renamingId, setRenamingId] = useState<string | null>(null)
  const [renameVal, setRenameVal] = useState('')

  const [addText, setAddText] = useState('')
  const [adding, setAdding] = useState(false)
  const [addMsg, setAddMsg] = useState('')

  const load = useCallback((t: string) => {
    adminPushGroupsApi.list(t).then((r) => setGroups(r.groups)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
  }, [router])

  const loadDetail = useCallback((t: string, id: string) => {
    setDetailErr('')
    adminPushGroupsApi.get(t, id).then((r) => setDetail(r)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setDetailErr(e?.message || '載入群組明細失敗')
    })
  }, [router])

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setTok(t); load(t)
  }, [router, load])

  useEffect(() => {
    if (token && selectedId) loadDetail(token, selectedId)
    else setDetail(null)
  }, [token, selectedId, loadDetail])

  async function createGroup() {
    if (!token) return
    const name = newName.trim()
    if (!name) { setErr('請輸入群組名稱'); return }
    setCreating(true); setErr('')
    try {
      await adminPushGroupsApi.create(token, name)
      setNewName('')
      load(token)
    } catch (e: any) { setErr(e?.message || '新增失敗') }
    finally { setCreating(false) }
  }

  async function saveRename(id: string) {
    if (!token) return
    const name = renameVal.trim()
    if (!name) { setErr('請輸入群組名稱'); return }
    try {
      await adminPushGroupsApi.rename(token, id, name)
      setRenamingId(null)
      load(token)
    } catch (e: any) { setErr(e?.message || '改名失敗') }
  }

  async function delGroup(id: string) {
    if (!token || !window.confirm('確定刪除此群組？')) return
    try {
      await adminPushGroupsApi.del(token, id)
      if (selectedId === id) setSelectedId(null)
      load(token)
    } catch (e: any) { setErr(e?.message || '刪除失敗') }
  }

  async function addMembers() {
    if (!token || !selectedId) return
    const identifiers = addText.split('\n').map((s) => s.trim()).filter(Boolean)
    if (identifiers.length === 0) { setAddMsg('請輸入帳號編碼或 Email'); return }
    setAdding(true); setAddMsg('')
    try {
      const r = await adminPushGroupsApi.addMembers(token, selectedId, identifiers)
      setAddMsg(r.not_found.length > 0 ? `已加入 ${r.added}、查無：${r.not_found.join('、')}` : `已加入 ${r.added}、查無：無`)
      setAddText('')
      loadDetail(token, selectedId)
      load(token)
    } catch (e: any) { setAddMsg(e?.message || '加入失敗') }
    finally { setAdding(false) }
  }

  async function removeMember(userId: string) {
    if (!token || !selectedId) return
    try {
      await adminPushGroupsApi.removeMember(token, selectedId, userId)
      loadDetail(token, selectedId)
      load(token)
    } catch (e: any) { setDetailErr(e?.message || '移除失敗') }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 8px', fontSize: 24, fontWeight: 800 }}>帳號群組管理</h1>
      <p style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.7, margin: '0 0 16px', maxWidth: 640 }}>
        建立會員群組，供推播通知等功能指定發送對象。可用<b>帳號編碼</b>或 <b>Email</b> 批次加入成員。
      </p>

      {err && <div style={{ color: 'var(--hunt)', marginTop: 4, marginBottom: 12, fontSize: 13 }}>{err}</div>}

      <div style={{ display: 'flex', gap: 20, alignItems: 'flex-start', flexWrap: 'wrap' }}>
        {/* 左側：群組清單 */}
        <div style={{ flex: '1 1 320px', minWidth: 300 }}>
          <div style={card}>
            <div style={{ fontWeight: 800, marginBottom: 12 }}>新增群組</div>
            <div style={{ display: 'flex', gap: 8 }}>
              <input style={inp} value={newName} onChange={(e) => setNewName(e.target.value)} placeholder="群組名稱" />
              <button onClick={createGroup} disabled={creating} style={primaryBtn}>{creating ? '新增中…' : '新增'}</button>
            </div>
          </div>

          {groups === null ? (
            <div style={{ color: 'var(--tx-faint)', marginTop: 18 }}>載入中…</div>
          ) : groups.length === 0 ? (
            <div style={{ color: 'var(--tx-faint)', marginTop: 18 }}>尚無群組。</div>
          ) : (
            <div style={{ marginTop: 18, display: 'flex', flexDirection: 'column', gap: 10 }}>
              {groups.map((g) => (
                <div
                  key={g.id}
                  onClick={() => setSelectedId(g.id)}
                  style={{ ...row, cursor: 'pointer', borderColor: selectedId === g.id ? 'var(--fug)' : 'var(--line)' }}
                >
                  {renamingId === g.id ? (
                    <div style={{ display: 'flex', gap: 8, flex: 1, minWidth: 0 }} onClick={(e) => e.stopPropagation()}>
                      <input style={inp} value={renameVal} onChange={(e) => setRenameVal(e.target.value)} autoFocus />
                      <button onClick={() => saveRename(g.id)} style={primaryBtn}>儲存</button>
                      <button onClick={() => setRenamingId(null)} style={ghostBtn}>取消</button>
                    </div>
                  ) : (
                    <>
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontWeight: 800 }}>{g.name}</div>
                        <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 3 }}>{g.member_count} 人</div>
                      </div>
                      <div style={{ display: 'flex', gap: 8, flexShrink: 0 }} onClick={(e) => e.stopPropagation()}>
                        <button onClick={() => { setRenamingId(g.id); setRenameVal(g.name) }} style={ghostBtn}>編輯名稱</button>
                        <button onClick={() => delGroup(g.id)} style={{ ...ghostBtn, color: 'var(--hunt)' }}>刪除</button>
                      </div>
                    </>
                  )}
                </div>
              ))}
            </div>
          )}
        </div>

        {/* 右側：選中群組的成員 */}
        <div style={{ flex: '1 1 380px', minWidth: 320 }}>
          {!selectedId ? (
            <div style={{ color: 'var(--tx-faint)', marginTop: 4 }}>請先選擇左側群組</div>
          ) : (
            <div>
              <div style={card}>
                <div style={{ fontWeight: 800, marginBottom: 12 }}>加入成員</div>
                <p style={{ fontSize: 12.5, color: 'var(--tx-faint)', margin: '0 0 8px' }}>貼上帳號編碼或 Email，一行一個。</p>
                <textarea
                  style={{ ...inp, height: 100, resize: 'vertical', fontFamily: 'inherit' }}
                  value={addText}
                  onChange={(e) => setAddText(e.target.value)}
                  placeholder={'A0001\nuser@example.com'}
                />
                <div style={{ display: 'flex', gap: 10, marginTop: 10, alignItems: 'center' }}>
                  <button onClick={addMembers} disabled={adding} style={primaryBtn}>{adding ? '加入中…' : '加入成員'}</button>
                  {addMsg && <span style={{ fontSize: 12.5, color: 'var(--tx-dim)' }}>{addMsg}</span>}
                </div>
              </div>

              {detailErr && <div style={{ color: 'var(--hunt)', marginTop: 10, fontSize: 13 }}>{detailErr}</div>}

              {detail === null ? (
                <div style={{ color: 'var(--tx-faint)', marginTop: 18 }}>載入中…</div>
              ) : detail.members.length === 0 ? (
                <div style={{ color: 'var(--tx-faint)', marginTop: 18 }}>尚無成員。</div>
              ) : (
                <div style={{ marginTop: 18, display: 'flex', flexDirection: 'column', gap: 10 }}>
                  {detail.members.map((m) => (
                    <div key={m.user_id} style={row}>
                      <div style={{ minWidth: 0 }}>
                        <div style={{ fontWeight: 800 }}>{m.account_code} · {m.name}</div>
                        <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 3 }}>{m.email}</div>
                      </div>
                      <div style={{ display: 'flex', gap: 8, flexShrink: 0 }}>
                        <button onClick={() => removeMember(m.user_id)} style={{ ...ghostBtn, color: 'var(--hunt)' }}>移除</button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

const inp: React.CSSProperties = { background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 8, padding: '8px 10px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit', boxSizing: 'border-box' }
const primaryBtn: React.CSSProperties = { background: 'var(--fug)', color: 'var(--fug-ink)', fontWeight: 800, border: 'none', borderRadius: 9, padding: '9px 18px', cursor: 'pointer', fontSize: 14, fontFamily: 'inherit', flexShrink: 0 }
const ghostBtn: React.CSSProperties = { background: 'transparent', color: 'var(--tx-dim)', border: '1px solid var(--line-2)', borderRadius: 9, padding: '9px 16px', cursor: 'pointer', fontSize: 13.5, fontFamily: 'inherit' }
const card: React.CSSProperties = { border: '1px solid var(--line)', borderRadius: 14, padding: 18, marginTop: 4, marginBottom: 4 }
const row: React.CSSProperties = { display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: 12, border: '1px solid var(--line)', borderRadius: 12, padding: '12px 14px' }
