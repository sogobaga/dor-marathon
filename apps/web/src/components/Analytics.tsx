'use client'

import { useEffect } from 'react'
import { initGA } from '@/lib/analytics'

// 載入 GA4（只在正式站生效，見 lib/analytics）。放 layout body，全站掛一次。
export default function Analytics() {
  useEffect(() => { initGA() }, [])
  return null
}
