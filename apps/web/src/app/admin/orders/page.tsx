'use client'

import { useCallback, useEffect, useState } from 'react'
import { useRouter } from 'next/navigation'
import { adminRacesApi, adminOrdersApi, adminPaymentsApi, adminInvoiceApi, type Race, type OrderRow, type OrderDetail, type RefundRow, type EcpayEnvCheck, type EInvoiceDetail, type EInvoiceAllowance, type ExportOrderRow, type InvoiceVerifyResult } from '@/lib/api'
import { getToken, clearToken } from '@/lib/adminAuth'
import * as XLSX from 'xlsx'

const REFUND_STATUS_LABEL: Record<string, { t: string; c: string }> = {
  pending: { t: '處理中', c: 'var(--gold)' },
  success: { t: '已退款（API）', c: 'var(--fug)' },
  failed: { t: '失敗', c: 'var(--hunt)' },
  manual_required: { t: '待人工處理', c: 'var(--gold)' },
  manual_done: { t: '已人工退款', c: 'var(--fug)' },
}

const STATUS_LABEL: Record<string, { t: string; c: string }> = {
  paid: { t: '已付款', c: 'var(--fug)' },
  pending: { t: '待付款', c: 'var(--gold)' },
  cancelled: { t: '已取消', c: 'var(--tx-faint)' },
  refunded: { t: '已退款', c: 'var(--hunt)' },
}
const ITEM_LABEL: Record<string, string> = {
  entry: '報名費', addon: '加購',
  vip_month: 'VIP 月費訂閱', vip_year: 'VIP 年費訂閱', // VIP 訂閱訂單（無賽事，見 orders.race_id 可空）
}

const INVOICE_BUYER_LABEL: Record<string, string> = {
  personal: '二聯式（個人）',
  company: '三聯式（公司，可報帳）',
  donation: '捐贈發票',
}

// 電子發票（見 services/api/internal/einvoice）實際開立狀態；跟上面 INVOICE_BUYER_LABEL（報名時填的買受人快照）是兩件事。
const EINVOICE_STATUS_LABEL: Record<string, { t: string; c: string }> = {
  pending: { t: '待處理', c: 'var(--tx-faint)' },
  issuing: { t: '開立中', c: 'var(--gold)' },
  issued: { t: '已開立', c: 'var(--fug)' },
  failed: { t: '失敗', c: 'var(--hunt)' },
  skipped: { t: '免開立', c: 'var(--tx-faint)' },
  void: { t: '已作廢', c: 'var(--tx-faint)' },
}

function ntd(c: number) {
  return 'NT$ ' + Math.round(c / 100).toLocaleString('zh-TW')
}

// 站內慣例格式 MM/DD HH:mm（同 RaceRankingScreen.fmtDateTime）；未付款 paid_at 為 null/undefined 時顯示「—」
function fmtDT(iso?: string | null) {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getMonth() + 1}/${d.getDate()} ${p(d.getHours())}:${p(d.getMinutes())}`
}

function invoiceText(inv: OrderRow['invoice']): string {
  if (!inv) return ''
  const label = INVOICE_BUYER_LABEL[inv.buyer_type] ?? inv.buyer_type
  if (inv.buyer_type === 'company') return `${label}｜統編 ${inv.tax_id || '—'}｜抬頭 ${inv.title || '—'}`
  if (inv.buyer_type === 'personal') return `${label}｜載具 ${inv.carrier_type === 'mobile' && inv.carrier_id ? inv.carrier_id : '雲端發票存證'}`
  if (inv.buyer_type === 'donation') return `${label}｜愛心碼 ${inv.love_code || '—'}`
  return label
}

// 同 invoiceText，但吃匯出訂單那組扁平欄位（buyer_type/tax_id/title/carrier_id/love_code，沒有 carrier_type
// 因為匯出目前只收手機條碼載具，見 ExportOrderRow／後端 orderIdentityColsSQL 未帶 carrier_type）。
function exportInvoiceBuyerText(o: ExportOrderRow): string {
  if (!o.buyer_type) return ''
  const label = INVOICE_BUYER_LABEL[o.buyer_type] ?? o.buyer_type
  if (o.buyer_type === 'company') return `${label}｜統編 ${o.tax_id || '—'}｜抬頭 ${o.title || '—'}`
  if (o.buyer_type === 'personal') return `${label}｜手機條碼 ${o.carrier_id || '雲端發票存證'}`
  if (o.buyer_type === 'donation') return `${label}｜愛心碼 ${o.love_code || '—'}`
  return label
}

export default function AdminOrdersPage() {
  const router = useRouter()
  const [races, setRaces] = useState<Race[]>([])
  const [raceID, setRaceID] = useState('')
  const [status, setStatus] = useState('')
  const [hideVirtual, setHideVirtual] = useState(true) // 預設隱藏虛擬選手，比照會員管理頁
  const [rows, setRows] = useState<OrderRow[] | null>(null)
  const [err, setErr] = useState('')
  const [token, setTok] = useState<string | null>(null)
  const [expanded, setExpanded] = useState<Record<string, OrderDetail | null>>({})
  const [refunds, setRefunds] = useState<Record<string, RefundRow[]>>({})
  const [invoices, setInvoices] = useState<Record<string, { invoice: EInvoiceDetail | null; allowances: EInvoiceAllowance[] }>>({})
  // 「驗證載具」（見 einvoice/verify.go）：對每筆訂單目前存的手機條碼/愛心碼即時查驗一次，
  // 結果只存在畫面上（不落地），orderID → 查驗結果；busy 用共用的 busy 狀態避免重複點擊。
  const [carrierChecks, setCarrierChecks] = useState<Record<string, InvoiceVerifyResult | 'busy' | 'error'>>({})
  const [busy, setBusy] = useState<string>('') // 進行中的 orderID/refundID，避免重複點擊
  const [envCheck, setEnvCheck] = useState<EcpayEnvCheck | null>(null)
  const [envCheckErr, setEnvCheckErr] = useState('')
  const [exporting, setExporting] = useState(false)
  const [exportingRace, setExportingRace] = useState(false) // 「匯出賽事訂單（含加購）」進行中旗標，與上面通用 exporting 分開
  // 從「電子發票」列表頁點「查看訂單」帶 ?order_id= 過來：載入後自動展開該筆（若不在目前篩選/分頁範圍內則額外補抓一筆插到最前面）。
  // 用 lazy state 直接讀 window.location.search（而非 useSearchParams），避免多帶一個 Suspense 邊界。
  const [highlightOrderID] = useState<string>(() => {
    if (typeof window === 'undefined') return ''
    return new URLSearchParams(window.location.search).get('order_id') || ''
  })
  const [highlightDone, setHighlightDone] = useState(false)

  useEffect(() => {
    const t = getToken()
    if (!t) { router.replace('/admin/login'); return }
    setTok(t)
    adminRacesApi.list(t).then((r) => setRaces(r.races)).catch((e) => {
      if (e?.status === 401) { clearToken(); router.replace('/admin/login') } else setErr(e?.message || '載入失敗')
    })
    const origin = typeof window !== 'undefined' ? window.location.origin : ''
    adminPaymentsApi.envCheck(t, origin).then(setEnvCheck).catch((e) => setEnvCheckErr(e?.message || '金流環境診斷載入失敗'))
  }, [router])

  const loadRefunds = useCallback((orderID: string) => {
    const t = getToken()
    if (!t) return
    adminPaymentsApi.listRefunds(t, orderID)
      .then((r) => setRefunds((rs) => ({ ...rs, [orderID]: r.refunds })))
      .catch(() => {})
  }, [])

  const loadInvoice = useCallback((orderID: string) => {
    const t = getToken()
    if (!t) return
    adminInvoiceApi.get(t, orderID)
      .then((r) => setInvoices((m) => ({ ...m, [orderID]: { invoice: r.invoice, allowances: r.allowances } })))
      .catch(() => {})
  }, [])

  const load = useCallback((rid: string, st: string, hideVirtualArg: boolean) => {
    const t = getToken()
    if (!t) return
    setRows(null)
    adminOrdersApi.list(t, { race_id: rid || undefined, status: st || undefined, hideVirtual: hideVirtualArg })
      .then((r) => {
        setRows(r.orders)
        if (!highlightOrderID || highlightDone) return
        setHighlightDone(true)
        const hit = r.orders.find((x) => x.id === highlightOrderID)
        if (hit) {
          setExpanded((e) => ({ ...e, [hit.id]: null }))
          adminOrdersApi.get(t, hit.id).then(({ order }) => setExpanded((e) => ({ ...e, [order.id]: order }))).catch(() => {})
          loadRefunds(hit.id); loadInvoice(hit.id)
        } else {
          // 不在目前篩選/分頁範圍內（例如換了篩選、或超過預設 100 筆）——直接補抓這一筆插到列表最前面
          adminOrdersApi.get(t, highlightOrderID).then(({ order }) => {
            setRows((rs) => (rs?.some((x) => x.id === order.id) ? rs : [order, ...(rs ?? [])]))
            setExpanded((e) => ({ ...e, [order.id]: order }))
            loadRefunds(order.id); loadInvoice(order.id)
          }).catch((e) => setErr(e?.message || '找不到指定訂單'))
        }
      })
      .catch((e) => setErr(e?.message || '載入失敗'))
  }, [highlightOrderID, highlightDone, loadRefunds, loadInvoice])

  useEffect(() => { load(raceID, status, hideVirtual) }, [raceID, status, hideVirtual, load])

  async function toggle(o: OrderRow) {
    if (expanded[o.id] !== undefined) {
      setExpanded((e) => { const n = { ...e }; delete n[o.id]; return n })
      return
    }
    if (!token) return
    try {
      const { order } = await adminOrdersApi.get(token, o.id)
      setExpanded((e) => ({ ...e, [o.id]: order }))
      loadRefunds(o.id)
      loadInvoice(o.id)
    } catch (e: any) { setErr(e?.message || '載入明細失敗') }
  }

  // 發票開立/作廢/同步查詢的回應都帶回最新 invoice 快照——順便同步列表列上的 invoice_status/invoice_number，
  // 不必整批重新 GET /admin/orders。
  function applyInvoiceUpdate(orderID: string, inv: EInvoiceDetail) {
    setInvoices((m) => ({ ...m, [orderID]: { invoice: inv, allowances: m[orderID]?.allowances ?? [] } }))
    setRows((rs) => rs?.map((x) => x.id === orderID ? { ...x, invoice_status: inv.invoice_status, invoice_number: inv.invoice_number } : x) ?? rs)
  }

  async function issueInvoice(orderID: string) {
    if (!token || busy) return
    setBusy(orderID)
    try {
      const r = await adminInvoiceApi.issue(token, orderID)
      applyInvoiceUpdate(orderID, r.invoice)
    } catch (e: any) { setErr(e?.message || '開立發票失敗') } finally { setBusy('') }
  }

  async function syncInvoice(orderID: string) {
    if (!token || busy) return
    setBusy(orderID)
    try {
      const r = await adminInvoiceApi.sync(token, orderID)
      applyInvoiceUpdate(orderID, r.invoice)
    } catch (e: any) { setErr(e?.message || '同步查詢失敗') } finally { setBusy('') }
  }

  async function voidInvoice(orderID: string) {
    if (!token || busy) return
    const reason = window.prompt('作廢原因（必填，20 字以內）：', '')
    if (reason === null) return
    const trimmed = reason.trim()
    if (!trimmed) { setErr('作廢原因為必填'); return }
    if ([...trimmed].length > 20) { setErr('作廢原因請在 20 字以內'); return }
    if (!window.confirm('確定要作廢此張發票嗎？此動作無法復原。')) return
    setBusy(orderID)
    try {
      const r = await adminInvoiceApi.void(token, orderID, trimmed)
      applyInvoiceUpdate(orderID, r.invoice)
    } catch (e: any) { setErr(e?.message || '作廢發票失敗') } finally { setBusy('') }
  }

  async function manualAllowance(orderID: string) {
    if (!token || busy) return
    const amountStr = window.prompt('折讓金額（新台幣整數）：', '')
    if (amountStr === null) return
    const n = Number(amountStr.trim())
    if (!Number.isFinite(n) || n <= 0) { setErr('折讓金額格式錯誤'); return }
    const reason = window.prompt('折讓原因：', '')
    if (reason === null) return
    if (!window.confirm(`確定要對此發票開立 NT$ ${Math.round(n).toLocaleString('zh-TW')} 折讓嗎？`)) return
    setBusy(orderID)
    try {
      await adminInvoiceApi.allowance(token, orderID, Math.round(n), reason.trim())
      loadInvoice(orderID)
    } catch (e: any) { setErr(e?.message || '折讓失敗') } finally { setBusy('') }
  }

  // 「驗證載具」：對訂單目前存的手機條碼/愛心碼重打一次 ECPay CheckBarcode/CheckLoveCode
  // （見 einvoice/verify.go VerifyCarrier）。純唯讀查詢，不落地，失敗一律顯示「無法查驗」不擋操作。
  async function verifyCarrier(orderID: string) {
    if (!token) return
    setCarrierChecks((m) => ({ ...m, [orderID]: 'busy' }))
    try {
      const r = await adminInvoiceApi.verifyCarrier(token, orderID)
      setCarrierChecks((m) => ({ ...m, [orderID]: r }))
    } catch {
      setCarrierChecks((m) => ({ ...m, [orderID]: 'error' }))
    }
  }

  async function markPaid(o: OrderRow) {
    if (!token) return
    const ref = window.prompt(`標記訂單為已付款。\n可選填金流訂單號（payment_ref），留空亦可：`, '')
    if (ref === null) return
    try {
      await adminOrdersApi.markPaid(token, o.id, ref || undefined)
      setRows((rs) => rs?.map((x) => x.id === o.id ? { ...x, status: 'paid', payment_ref: ref || x.payment_ref } : x) ?? rs)
    } catch (e: any) { setErr(e?.message || '操作失敗') }
  }

  async function refundOrder(o: OrderRow) {
    if (!token || busy) return
    const amountStr = window.prompt(
      `退款金額（新台幣整數，NT$ ${Math.round(o.total_cents / 100).toLocaleString('zh-TW')} 為訂單總額）。\n留空＝退還剩餘可退全額：`,
      ''
    )
    if (amountStr === null) return
    const reason = window.prompt('退款原因（必填）：', '')
    if (reason === null) return
    if (!reason.trim()) { setErr('退款原因為必填'); return }
    let amountCents: number | undefined
    if (amountStr.trim()) {
      const n = Number(amountStr.trim())
      if (!Number.isFinite(n) || n <= 0 || !Number.isInteger(n)) { setErr('退款金額請填整數新台幣'); return }
      amountCents = Math.round(n * 100)
    }
    if (!window.confirm(`確定要對此訂單退款嗎？\n金額：${amountCents ? 'NT$ ' + Math.round(amountCents / 100).toLocaleString('zh-TW') : '剩餘可退全額'}\n原因：${reason}`)) return

    setBusy(o.id)
    try {
      const res = await adminPaymentsApi.createRefund(token, { order_id: o.id, amount_cents: amountCents, reason: reason.trim() })
      if (res.status === 'manual_required') {
        window.alert(`已建立人工退款紀錄，請完成銀行匯款後回來標記「已完成」。\n${res.note || ''}`)
      }
      const { order } = await adminOrdersApi.get(token, o.id)
      setExpanded((e) => ({ ...e, [o.id]: order }))
      setRows((rs) => rs?.map((x) => x.id === o.id ? { ...x, status: order.status } : x) ?? rs)
      loadRefunds(o.id)
    } catch (e: any) {
      setErr(e?.message || '退款操作失敗')
    } finally {
      setBusy('')
    }
  }

  async function manualDone(orderID: string, refundID: string) {
    if (!token || busy) return
    if (!window.confirm('確認已完成人工匯款，標記此筆退款為「已完成」？')) return
    setBusy(refundID)
    try {
      await adminPaymentsApi.markRefundManualDone(token, refundID)
      const { order } = await adminOrdersApi.get(token, orderID)
      setExpanded((e) => ({ ...e, [orderID]: order }))
      setRows((rs) => rs?.map((x) => x.id === orderID ? { ...x, status: order.status } : x) ?? rs)
      loadRefunds(orderID)
    } catch (e: any) {
      setErr(e?.message || '操作失敗')
    } finally {
      setBusy('')
    }
  }

  // 匯出目前篩選條件（賽事/狀態）下的「全部」訂單為 xlsx（不只當前頁）。
  // 取捨：後端 AdminListOrders 單頁 clamp 上限 200（service.ListOrders），故用 limit=200 迴圈翻頁拉到全量，
  // 而非一次把 limit 開超大——避免單次查詢/回應過重。品項明細（order_items）ListOrders 回應本來就沒帶，
  // 若要帶出需對每筆訂單多打一次 GetOrderDetail（N+1），量大時會很慢，故本匯出比照列表現有資料，不含逐筆品項明細。
  async function exportXlsx() {
    if (!token || exporting) return
    setExporting(true)
    setErr('')
    try {
      const all: OrderRow[] = []
      const pageSize = 200
      let offset = 0
      for (;;) {
        const r = await adminOrdersApi.list(token, { race_id: raceID || undefined, status: status || undefined, hideVirtual, limit: pageSize, offset })
        all.push(...r.orders)
        if (r.orders.length < pageSize) break
        offset += pageSize
      }
      if (all.length === 0) { window.alert('沒有符合篩選條件的訂單可匯出'); return }
      const rows = all.map((o) => ({
        '訂單編號': o.id,
        '會員名稱': o.user_name,
        'Email': o.user_email,
        '賽事名稱': o.race_title || 'VIP 訂閱',
        '金額(元)': Math.round(o.total_cents / 100),
        '狀態': STATUS_LABEL[o.status]?.t ?? o.status,
        '付款時間': fmtDT(o.paid_at),
        '建立時間': fmtDT(o.created_at),
        '付款參考': o.payment_ref || '',
        '發票資訊': invoiceText(o.invoice),
      }))
      const ws = XLSX.utils.json_to_sheet(rows)
      const wb = XLSX.utils.book_new()
      XLSX.utils.book_append_sheet(wb, ws, '訂單')
      const d = new Date()
      const p = (n: number) => String(n).padStart(2, '0')
      XLSX.writeFile(wb, `orders_${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}_${p(d.getHours())}${p(d.getMinutes())}.xlsx`)
    } catch (e: any) {
      setErr(e?.message || '匯出失敗')
    } finally {
      setExporting(false)
    }
  }

  // 匯出「單一賽事」訂單（含加購明細）——後台管明細用，2026-09-08 owner request：每列要看得到真實姓名/
  // 地址/手機/賽事/組別/加購品項數量/金額/總金額。改打專用 /admin/orders/export（後端已聚合好每筆訂單的
  // 品項小計，不必像上面 exportXlsx 一樣逐頁翻頁湊資料）。只在已選定賽事篩選時可用（race_id 必填）。
  async function exportRaceOrdersXlsx() {
    if (!token || exportingRace || !raceID) return
    setExportingRace(true)
    setErr('')
    try {
      const resp = await adminOrdersApi.export(token, { race_id: raceID, status: status || undefined, hideVirtual })
      if (resp.orders.length === 0) { window.alert('沒有符合篩選條件的訂單可匯出'); return }

      const orderRows = resp.orders.map((o) => ({
        '訂單編號': o.id,
        '建立時間': fmtDT(o.created_at),
        '付款時間': fmtDT(o.paid_at),
        '狀態': STATUS_LABEL[o.status]?.t ?? o.status,
        '會員帳號(Email)': o.user_email,
        '帳號 handle': o.user_handle,
        '顯示名稱': o.user_name,
        '真實姓名': o.real_name || '未填',
        '手機': o.phone || '未填',
        '地址': o.address || '未填',
        '賽事': resp.race.title,
        '組別(距離 km)': o.distance_km || '',
        '陣營': o.faction || '',
        '分組': o.group_name || '',
        // 寵物雲端馬拉松（2026-09-08，D6）：後端已用「；」join 好名稱，一般賽事一律空字串
        '寵物': o.pets_text || '',
        '報名費': Math.round(o.entry_cents / 100),
        '加購品項': o.addons.map((a) => `${a.name}×${a.qty}`).join('；'),
        '加購金額': Math.round(o.addon_cents / 100),
        '折扣': Math.round(o.discount_cents / 100),
        '總金額': Math.round(o.total_cents / 100),
        '發票狀態': o.invoice_status ? (EINVOICE_STATUS_LABEL[o.invoice_status]?.t ?? o.invoice_status) : '',
        '發票號碼': o.invoice_number || '',
        '發票買受人': exportInvoiceBuyerText(o),
      }))

      // 品項明細：報名費/折扣由 entry_cents/discount_cents 各自合成一列（非 0 才列出），加購每筆各自一列
      const itemRows: Record<string, string | number>[] = []
      resp.orders.forEach((o) => {
        if (o.entry_cents) {
          itemRows.push({
            '訂單編號': o.id, '會員帳號': o.user_email, '真實姓名': o.real_name || '未填',
            '品項類型': '報名費', '品項名稱': '報名費', '數量': 1,
            '單價': Math.round(o.entry_cents / 100), '小計': Math.round(o.entry_cents / 100),
          })
        }
        o.addons.forEach((a) => {
          itemRows.push({
            '訂單編號': o.id, '會員帳號': o.user_email, '真實姓名': o.real_name || '未填',
            '品項類型': '加購', '品項名稱': a.name, '數量': a.qty,
            '單價': Math.round(a.unit_price_cents / 100), '小計': Math.round(a.subtotal_cents / 100),
          })
        })
        if (o.discount_cents) {
          itemRows.push({
            '訂單編號': o.id, '會員帳號': o.user_email, '真實姓名': o.real_name || '未填',
            '品項類型': '折扣', '品項名稱': '折扣', '數量': 1,
            '單價': Math.round(o.discount_cents / 100), '小計': Math.round(o.discount_cents / 100),
          })
        }
      })

      // 寵物明細（D6 第三張 sheet）：每隻寵物一列（含晶片號碼）；非寵物賽事沒有 pets → 不產生這張 sheet
      // 後端刻意不另開頂層 pets 陣列（單一查詢，見 race/model.go ExportOrderRow.Pets 註解），
      // 這裡從 orders[].pets 攤平；審查抓到原本讀不存在的 resp.pets 導致這張 sheet 永遠不會產生。
      const petRows = resp.orders.flatMap((o) => (o.pets ?? []).map((p) => ({
        '訂單編號': o.id,
        '會員帳號': o.user_handle,
        '真實姓名': o.real_name || '未填',
        '序號': p.seq,
        '寵物名稱': p.name,
        '晶片號碼': p.chip_id || '',
      })))

      const wb = XLSX.utils.book_new()
      XLSX.utils.book_append_sheet(wb, XLSX.utils.json_to_sheet(orderRows), '訂單')
      XLSX.utils.book_append_sheet(wb, XLSX.utils.json_to_sheet(itemRows), '品項明細')
      if (petRows.length > 0) {
        XLSX.utils.book_append_sheet(wb, XLSX.utils.json_to_sheet(petRows), '寵物明細')
      }
      const d = new Date()
      const p = (n: number) => String(n).padStart(2, '0')
      const race = races.find((r) => r.id === raceID)
      const slug = race?.slug || raceID
      XLSX.writeFile(wb, `race_orders_${slug}_${d.getFullYear()}${p(d.getMonth() + 1)}${p(d.getDate())}_${p(d.getHours())}${p(d.getMinutes())}.xlsx`)
    } catch (e: any) {
      setErr(e?.message || '匯出失敗')
    } finally {
      setExportingRace(false)
    }
  }

  return (
    <div>
      <h1 style={{ margin: '0 0 18px', fontSize: 24, fontWeight: 800 }}>訂單管理</h1>

      {envCheckErr && <div style={{ color: 'var(--hunt)', padding: 12, marginBottom: 14 }}>金流環境診斷載入失敗：{envCheckErr}</div>}
      {envCheck && (
        <div style={{
          border: `1px solid ${!envCheck.resolve_ok ? 'var(--hunt)' : envCheck.would_charge_real_money ? 'var(--hunt)' : 'var(--line)'}`,
          borderRadius: 14, padding: 16, marginBottom: 18, background: 'var(--bg-1)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 10, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 11, fontWeight: 700, letterSpacing: '.08em', color: 'var(--tx-faint)', textTransform: 'uppercase' }}>金流環境診斷</span>
            {envCheck.resolve_ok ? (
              <span style={{
                background: envCheck.would_charge_real_money ? 'var(--hunt)' : 'var(--fug)',
                color: '#fff', fontWeight: 800, fontSize: 12, padding: '3px 10px', borderRadius: 999,
              }}>
                {envCheck.would_charge_real_money ? '⚠ 正式特店（會收真錢）' : '測試特店（不會收真錢）'}
              </span>
            ) : (
              <span style={{
                background: 'var(--hunt)', color: '#fff', fontWeight: 800, fontSize: 12, padding: '3px 10px', borderRadius: 999,
              }}>
                ⚠ 此網域未授權，切正式後結帳會被擋下
              </span>
            )}
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(220px, 1fr))', gap: '4px 20px', fontSize: 13, color: 'var(--tx-dim)' }}>
            <div>結帳來源 origin：<span style={{ color: 'var(--tx)' }}>{envCheck.received_origin || '（空）'}</span></div>
            <div>解析結果：<span style={{ color: envCheck.resolve_ok ? 'var(--tx)' : 'var(--hunt)' }}>{envCheck.resolve_ok ? '可解析' : '未授權（fail closed）'}</span></div>
            <div>結帳網址：<span style={{ color: 'var(--tx)' }}>{envCheck.resolved_action_url || '（不適用）'}</span></div>
            <div>特店編號：<span style={{ color: 'var(--tx)' }}>{envCheck.resolved_merchant_id || '（空）'}</span></div>
            <div>全域 ECPAY_ENV：<span style={{ color: 'var(--tx)' }}>{envCheck.global_ecpay_env}</span></div>
            <div>正式來源白名單：<span style={{ color: 'var(--tx)' }}>{envCheck.prod_origins.join('、') || '（空）'}</span></div>
            <div>
              正式三寶已設定：{' '}
              <span style={{ color: envCheck.prod_credentials_configured.merchant_id ? 'var(--fug)' : 'var(--hunt)' }}>MerchantID {envCheck.prod_credentials_configured.merchant_id ? '✓' : '✗'}</span>{' '}
              <span style={{ color: envCheck.prod_credentials_configured.hash_key ? 'var(--fug)' : 'var(--hunt)' }}>HashKey {envCheck.prod_credentials_configured.hash_key ? '✓' : '✗'}</span>{' '}
              <span style={{ color: envCheck.prod_credentials_configured.hash_iv ? 'var(--fug)' : 'var(--hunt)' }}>HashIV {envCheck.prod_credentials_configured.hash_iv ? '✓' : '✗'}</span>
            </div>
          </div>
          <div style={{ marginTop: 10, paddingTop: 10, borderTop: '1px solid var(--line)', fontSize: 11, color: 'var(--tx-faint)' }}>
            除錯參考（已不用於決定特店）：Host＝{envCheck.legacy_host_headers.host || '（空）'}，X-Forwarded-Host＝{envCheck.legacy_host_headers.x_forwarded_host || '（空）'}
          </div>
        </div>
      )}

      <div style={{ display: 'flex', gap: 10, marginBottom: 18, flexWrap: 'wrap', alignItems: 'center' }}>
        <select value={raceID} onChange={(e) => setRaceID(e.target.value)} style={{ ...inp, maxWidth: 260 }}>
          <option value="">全部賽事</option>
          {races.map((r) => <option key={r.id} value={r.id}>{r.title}</option>)}
        </select>
        <select value={status} onChange={(e) => setStatus(e.target.value)} style={{ ...inp, maxWidth: 160 }}>
          <option value="">全部狀態</option>
          <option value="pending">待付款</option>
          <option value="paid">已付款</option>
          <option value="cancelled">已取消</option>
          <option value="refunded">已退款</option>
        </select>
        <label style={{ display: 'flex', alignItems: 'center', gap: 6, fontSize: 13, color: 'var(--tx-dim)', cursor: 'pointer' }}>
          <input
            type="checkbox"
            checked={hideVirtual}
            onChange={(e) => setHideVirtual(e.target.checked)}
          />
          隱藏虛擬選手
        </label>
        <button
          onClick={exportXlsx} disabled={exporting || !rows?.length}
          style={{ ...exportBtn, opacity: exporting || !rows?.length ? 0.5 : 1, cursor: exporting || !rows?.length ? 'default' : 'pointer' }}
        >
          {exporting ? '匯出中…' : '匯出 xlsx'}
        </button>
        <button
          onClick={exportRaceOrdersXlsx} disabled={exportingRace || !raceID}
          title={!raceID ? '請先選擇單一賽事才能匯出' : undefined}
          style={{ ...exportBtn, opacity: exportingRace || !raceID ? 0.5 : 1, cursor: exportingRace || !raceID ? 'default' : 'pointer' }}
        >
          {exportingRace ? '匯出中…' : '匯出賽事訂單（含加購）'}
        </button>
      </div>

      {err && <div style={{ color: 'var(--hunt)', padding: 16 }}>{err}</div>}
      {!rows && !err && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>載入中…</div>}
      {rows && rows.length === 0 && <div style={{ color: 'var(--tx-dim)', padding: 16 }}>沒有符合的訂單</div>}

      {rows && rows.length > 0 && (
        <div style={{ border: '1px solid var(--line)', borderRadius: 14, overflow: 'hidden' }}>
        <div style={{ overflowX: 'auto' }}>
        <div style={{ minWidth: 860 }}>
          <Row head><C w={2}>會員</C><C w={2}>賽事</C><C w={1}>金額</C><C w={1}>狀態</C><C w={1.2}>付款時間</C><C w={1}>發票</C><C w={1}>操作</C></Row>
          {rows.map((o) => {
            const st = STATUS_LABEL[o.status] ?? { t: o.status, c: 'var(--tx-dim)' }
            const det = expanded[o.id]
            const ist = o.invoice_status ? (EINVOICE_STATUS_LABEL[o.invoice_status] ?? { t: o.invoice_status, c: 'var(--tx-dim)' }) : null
            return (
              <div key={o.id}>
                <Row>
                  <C w={2}>
                    <button onClick={() => toggle(o)} style={linkBtn}>
                      {expanded[o.id] !== undefined ? '▾ ' : '▸ '}{o.user_name}
                    </button>
                    {o.real_name && <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}> ({o.real_name})</span>}
                    <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{o.user_email}</div>
                  </C>
                  <C w={2}>{o.race_title || 'VIP 訂閱'}</C>
                  <C w={1}>{ntd(o.total_cents)}</C>
                  <C w={1}><span style={{ color: st.c }}>{st.t}</span></C>
                  <C w={1.2}><span style={{ fontSize: 13, color: o.paid_at ? 'var(--tx-dim)' : 'var(--tx-faint)' }}>{fmtDT(o.paid_at)}</span></C>
                  <C w={1}>
                    {ist ? (
                      <>
                        <span style={{ fontSize: 12, fontWeight: 700, color: ist.c }}>{ist.t}</span>
                        {o.invoice_number && <div style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{o.invoice_number}</div>}
                      </>
                    ) : <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>—</span>}
                  </C>
                  <C w={1}>
                    {o.status === 'pending'
                      ? <button onClick={() => markPaid(o)} style={payBtn}>標記已付</button>
                      : o.status === 'paid'
                        ? <button onClick={() => refundOrder(o)} disabled={busy === o.id} style={refundBtn}>{busy === o.id ? '處理中…' : '退款'}</button>
                        : <span style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{o.payment_ref || '—'}</span>}
                  </C>
                </Row>
                {det && (
                  <div style={{ padding: '10px 16px 14px 32px', background: 'var(--bg-1)', borderBottom: '1px solid var(--line)' }}>
                    {det.items.map((it, i) => (
                      <div key={i} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, color: 'var(--tx-dim)', padding: '3px 0' }}>
                        <span>{ITEM_LABEL[it.item_type] ?? it.item_type}{it.addon_name ? `：${it.addon_name}` : ''} × {it.qty}</span>
                        <span>{ntd(it.subtotal_cents)}</span>
                      </div>
                    ))}
                    {det.paid_at && <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 6 }}>付款時間：{new Date(det.paid_at).toLocaleString('zh-TW')}{det.payment_ref ? ` · 金流號 ${det.payment_ref}` : ''}</div>}

                    {/* 真實個資（2026-09-08 owner request）：僅後台訂單管理明細可見，一律不進會員可見端點 */}
                    <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 8 }}>真實姓名：{det.real_name || '未填'}</div>
                    <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>手機：{det.phone || '未填'}</div>
                    <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>地址：{det.address || '未填'}</div>
                    <div style={{ fontSize: 12, color: 'var(--tx-dim)' }}>
                      組別：{det.distance_km ? `${det.distance_km} km` : '未填'}・陣營：{det.faction || '未填'}・分組：{det.group_name || '未填'}
                    </div>
                    {/* 寵物雲端馬拉松（2026-09-08）：僅寵物賽事訂單有 pets */}
                    {det.pets && det.pets.length > 0 && (
                      <div style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4 }}>
                        寵物：{det.pets.map((p) => `${p.name}${p.chip_id ? `（晶片 ${p.chip_id}）` : ''}`).join('、')}
                      </div>
                    )}

                    <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 6 }}>
                      發票資訊：
                      {det.invoice ? (
                        <>
                          {INVOICE_BUYER_LABEL[det.invoice.buyer_type] ?? det.invoice.buyer_type}
                          {det.invoice.buyer_type === 'company' && ` · 統編 ${det.invoice.tax_id || '—'} · 抬頭 ${det.invoice.title || '—'}`}
                          {det.invoice.buyer_type === 'personal' && ` · 載具 ${det.invoice.carrier_type === 'mobile' && det.invoice.carrier_id ? det.invoice.carrier_id : '雲端發票存證'}`}
                          {det.invoice.buyer_type === 'donation' && ` · 愛心碼 ${det.invoice.love_code || '—'}`}
                          {/* 「驗證載具」（見 einvoice/verify.go）：只有手機條碼載具/捐贈愛心碼才有可查驗的號碼——
                              雲端發票存證（無載具）或公司統編走不同的驗證 API（CheckCompanyIdentifier，本次不做），不顯示按鈕 */}
                          {((det.invoice.buyer_type === 'personal' && det.invoice.carrier_type === 'mobile' && det.invoice.carrier_id) ||
                            (det.invoice.buyer_type === 'donation' && det.invoice.love_code)) && (
                            <>
                              {' '}
                              <button
                                onClick={() => verifyCarrier(o.id)}
                                disabled={carrierChecks[o.id] === 'busy'}
                                style={{ ...smallBtn, padding: '1px 8px', fontSize: 10.5 }}
                              >
                                {carrierChecks[o.id] === 'busy' ? '查驗中…' : '驗證載具'}
                              </button>
                              {(() => {
                                const c = carrierChecks[o.id]
                                if (!c || c === 'busy') return null
                                if (c === 'error' || !c.checked) {
                                  return <span style={{ color: 'var(--tx-faint)' }}> ⚪ 無法查驗{c !== 'error' && c.message ? `（${c.message}）` : ''}</span>
                                }
                                return c.exists
                                  ? <span style={{ color: 'var(--fug)' }}> ✓ 存在</span>
                                  : <span style={{ color: 'var(--hunt)' }}> ✗ 查無（{c.message}）</span>
                              })()}
                            </>
                          )}
                        </>
                      ) : '無發票資料'}
                    </div>

                    {/* 電子發票（見 services/api/internal/einvoice）：這是實際向綠界開立的結果；上面「發票資訊」只是報名時填的買受人快照 */}
                    {(() => {
                      const invData = invoices[o.id]
                      const inv = invData?.invoice
                      return (
                        <div style={{ marginTop: 10, borderTop: '1px solid var(--line)', paddingTop: 8 }}>
                          <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 4, letterSpacing: '.05em', textTransform: 'uppercase' }}>電子發票（綠界）</div>
                          {!invData ? (
                            <div style={{ fontSize: 12, color: 'var(--tx-faint)' }}>載入中…</div>
                          ) : !inv ? (
                            <div style={{ display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
                              <span style={{ fontSize: 12, color: 'var(--tx-faint)' }}>尚無發票資料</span>
                              {o.status === 'paid' && (
                                <button onClick={() => issueInvoice(o.id)} disabled={busy === o.id} style={smallBtn}>
                                  {busy === o.id ? '處理中…' : '開立'}
                                </button>
                              )}
                            </div>
                          ) : (
                            <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', fontSize: 12 }}>
                                <span style={{ fontWeight: 700, color: (EINVOICE_STATUS_LABEL[inv.invoice_status] ?? { c: 'var(--tx-dim)' }).c }}>
                                  {EINVOICE_STATUS_LABEL[inv.invoice_status]?.t ?? inv.invoice_status}
                                </span>
                                {inv.invoice_number && <span style={{ color: 'var(--tx)' }}>{inv.invoice_number}</span>}
                                {inv.invoice_date && <span style={{ color: 'var(--tx-faint)' }}>{inv.invoice_date}</span>}
                                {inv.random_number && <span style={{ color: 'var(--tx-faint)' }}>隨機碼 {inv.random_number}</span>}
                                {inv.ecpay_env && (
                                  <span style={{
                                    background: inv.ecpay_env === 'prod' ? 'var(--hunt)' : 'var(--tx-faint)',
                                    color: '#fff', fontSize: 10, fontWeight: 700, padding: '2px 6px', borderRadius: 999,
                                  }}>{inv.ecpay_env === 'prod' ? '正式' : '測試'}</span>
                                )}
                                {inv.attempts > 0 && <span style={{ color: 'var(--tx-faint)' }}>嘗試 {inv.attempts} 次</span>}
                              </div>
                              {inv.skip_reason && <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>略過原因：{inv.skip_reason}</div>}
                              {inv.last_error && <div style={{ fontSize: 11, color: 'var(--hunt)' }}>錯誤：{inv.last_error}</div>}
                              {inv.void_reason && <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>作廢原因：{inv.void_reason}</div>}
                              {inv.remain_allowance_ntd != null && <div style={{ fontSize: 11, color: 'var(--tx-faint)' }}>剩餘可折讓 {ntd(inv.remain_allowance_ntd * 100)}</div>}

                              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                                {(inv.invoice_status === 'pending' || inv.invoice_status === 'failed' || inv.invoice_status === 'skipped') && (
                                  <button onClick={() => issueInvoice(o.id)} disabled={busy === o.id} style={smallBtn}>
                                    {busy === o.id ? '處理中…' : '開立／重試'}
                                  </button>
                                )}
                                <button onClick={() => syncInvoice(o.id)} disabled={busy === o.id} style={smallBtn}>同步查詢</button>
                                {inv.invoice_status === 'issued' && (
                                  inv.buyer_type === 'donation' ? (
                                    <span style={{ fontSize: 11, color: 'var(--tx-faint)', alignSelf: 'center' }}>捐贈發票不可作廢</span>
                                  ) : (
                                    <button onClick={() => voidInvoice(o.id)} disabled={busy === o.id} style={{ ...smallBtn, color: 'var(--hunt)', borderColor: 'var(--hunt)' }}>作廢</button>
                                  )
                                )}
                                {inv.invoice_status === 'issued' && (
                                  <button onClick={() => manualAllowance(o.id)} disabled={busy === o.id} style={smallBtn}>手動折讓</button>
                                )}
                              </div>

                              {invData.allowances.length > 0 && (
                                <div style={{ marginTop: 2 }}>
                                  <div style={{ fontSize: 10, color: 'var(--tx-faint)', marginBottom: 3, letterSpacing: '.05em', textTransform: 'uppercase' }}>折讓紀錄</div>
                                  <div style={{ overflowX: 'auto' }}>
                                    <div style={{ minWidth: 460 }}>
                                      {invData.allowances.map((a) => (
                                        <div key={a.id} style={{ display: 'flex', justifyContent: 'space-between', gap: 8, fontSize: 12, color: 'var(--tx-dim)', padding: '3px 0' }}>
                                          <span>
                                            {ntd(a.amount_ntd * 100)} · {a.allowance_no || '—'}{a.allowance_date ? ` · ${a.allowance_date.slice(0, 10)}` : ''}
                                            {a.reason ? ` · ${a.reason}` : ''}
                                            {a.last_error ? ` · ${a.last_error}` : ''}
                                          </span>
                                          <span style={{ color: a.status === 'success' ? 'var(--fug)' : a.status === 'failed' ? 'var(--hunt)' : 'var(--gold)' }}>
                                            {a.status === 'success' ? '成功' : a.status === 'failed' ? '失敗' : '處理中'}
                                          </span>
                                        </div>
                                      ))}
                                    </div>
                                  </div>
                                </div>
                              )}
                            </div>
                          )}
                        </div>
                      )
                    })()}

                    {(refunds[o.id]?.length ?? 0) > 0 && (
                      <div style={{ marginTop: 10, borderTop: '1px solid var(--line)', paddingTop: 8 }}>
                        <div style={{ fontSize: 11, color: 'var(--tx-faint)', marginBottom: 4, letterSpacing: '.05em', textTransform: 'uppercase' }}>退款紀錄</div>
                        {refunds[o.id]!.map((rf) => {
                          const rst = REFUND_STATUS_LABEL[rf.status] ?? { t: rf.status, c: 'var(--tx-dim)' }
                          return (
                            <div key={rf.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8, fontSize: 12, color: 'var(--tx-dim)', padding: '4px 0' }}>
                              <span>
                                {ntd(rf.amount_cents)} · {rf.method === 'api' ? '信用卡 API' : '人工'} ·{' '}
                                <span style={{ color: rst.c }}>{rst.t}</span>
                                {rf.reason ? ` · ${rf.reason}` : ''}
                                {rf.ecpay_rtn_msg ? ` · ${rf.ecpay_rtn_msg}` : ''}
                              </span>
                              {rf.status === 'manual_required' && (
                                <button onClick={() => manualDone(o.id, rf.id)} disabled={busy === rf.id} style={smallBtn}>
                                  {busy === rf.id ? '處理中…' : '標記已完成'}
                                </button>
                              )}
                            </div>
                          )
                        })}
                      </div>
                    )}
                  </div>
                )}
              </div>
            )
          })}
        </div>
        </div>
        </div>
      )}
    </div>
  )
}

function Row({ children, head }: { children: React.ReactNode; head?: boolean }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px', borderBottom: '1px solid var(--line)',
      background: head ? 'var(--bg-1)' : 'transparent',
      fontSize: head ? 11 : 14, letterSpacing: head ? '.08em' : undefined,
      color: head ? 'var(--tx-faint)' : 'var(--tx)', textTransform: head ? 'uppercase' : 'none',
    }}>{children}</div>
  )
}
function C({ children, w }: { children: React.ReactNode; w: number }) {
  return <div style={{ flex: w, minWidth: 0 }}>{children}</div>
}

const inp: React.CSSProperties = {
  background: 'var(--bg-2)', border: '1px solid var(--line-2)', borderRadius: 10,
  padding: '10px 12px', color: 'var(--tx)', fontSize: 14, width: '100%', fontFamily: 'inherit',
}
const payBtn: React.CSSProperties = {
  background: 'var(--gold)', color: '#fff', fontWeight: 700, border: 'none',
  borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
}
const refundBtn: React.CSSProperties = {
  background: 'var(--hunt)', color: '#fff', fontWeight: 700, border: 'none',
  borderRadius: 8, padding: '6px 12px', cursor: 'pointer', fontSize: 13,
}
const smallBtn: React.CSSProperties = {
  background: 'var(--bg-2)', color: 'var(--tx)', fontWeight: 600, border: '1px solid var(--line-2)',
  borderRadius: 6, padding: '3px 8px', cursor: 'pointer', fontSize: 11, whiteSpace: 'nowrap',
}
const linkBtn: React.CSSProperties = {
  background: 'none', border: 'none', color: 'var(--tx)', cursor: 'pointer', fontSize: 14, fontWeight: 600, padding: 0,
}
const exportBtn: React.CSSProperties = {
  background: 'none', border: '1px solid var(--fug)', color: 'var(--fug)', borderRadius: 8,
  padding: '9px 16px', cursor: 'pointer', fontSize: 13, fontWeight: 700, fontFamily: 'inherit',
}
