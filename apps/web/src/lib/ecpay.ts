// 動態建立 hidden 表單並 POST 到綠界（瀏覽器導去付款頁）。報名完成頁與個人資訊頁共用。
export function submitEcpayForm(actionURL: string, params: Record<string, string>) {
  const form = document.createElement('form')
  form.method = 'POST'
  form.action = actionURL
  form.acceptCharset = 'UTF-8'
  for (const [k, v] of Object.entries(params)) {
    const input = document.createElement('input')
    input.type = 'hidden'
    input.name = k
    input.value = v
    form.appendChild(input)
  }
  document.body.appendChild(form)
  form.submit()
}
