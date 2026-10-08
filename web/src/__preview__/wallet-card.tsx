/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
/**
 * 钱包「添加资金」卡片的本地预览入口。
 *
 * 用途：不改生产、也不需要登录，就能看到改动后的真实渲染结果。
 * 它挂载的是线上那个真组件（features/wallet/components/recharge-form-card），
 * 用的是同一份 Tailwind 样式和同一套 i18n 文案 —— 只是数据换成写死的样例。
 *
 * 运行（在 web/ 目录下）：
 *   bunx rsbuild build --config rsbuild.preview.config.ts
 *   然后起个静态服务指向 web/preview-dist/，打开 preview.html
 *
 * 注意：headless Chrome 的 --window-size 有 500px 下限，要验证手机视口
 * （如 390px）必须用 CDP 的 Emulation.setDeviceMetricsOverride，否则页面按
 * 500px 布局、只是图片被裁窄，看起来像溢出、其实不是。
 */
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'

import '@/i18n/config'

import '@/styles/index.css'

import { RechargeFormCard } from '@/features/wallet/components/recharge-form-card'
import type { TopupInfo } from '@/features/wallet/types'
import { useSystemConfigStore } from '@/stores/system-config-store'

// 对齐线上的货币口径（线上 general_setting.quota_display_type = CNY，汇率 1）
const state = useSystemConfigStore.getState()
useSystemConfigStore.setState({
  config: {
    ...state.config,
    currency: {
      ...state.config.currency,
      displayInCurrency: true,
      quotaDisplayType: 'CNY',
      usdExchangeRate: 1,
      quotaPerUnit: 500000,
    },
  },
})

// 线上两处开关的现值：兑换码已启用、合规已确认
const topupInfo: TopupInfo = {
  enable_redemption: true,
  payment_compliance_confirmed: true,
  enable_online_topup: false,
  enable_stripe_topup: false,
  pay_methods: [],
  min_topup: 0,
  stripe_min_topup: 0,
  amount_options: [],
  discount: {},
}

export function Case({ label, code }: { label: string; code: string }) {
  return (
    <div className='space-y-2'>
      <div className='text-muted-foreground font-mono text-xs'>{label}</div>
      <RechargeFormCard
        topupInfo={topupInfo}
        redemptionCode={code}
        onRedemptionCodeChange={() => {}}
        onRedeem={() => {}}
        preview={null}
        onConfirmRedemption={() => {}}
        onCancelPreview={() => {}}
        redeeming={false}
        confirmingRedemption={false}
        onOpenBilling={() => {}}
      />
    </div>
  )
}

// 线上左侧卡片列约 663px 宽（max-w-7xl 两栏布局）；用 max-w 而非固定宽，窄屏才能看出响应式
const root = document.querySelector('#root')
if (!root) throw new Error('Preview root element is missing')

createRoot(root).render(
  <StrictMode>
    <div className='bg-background text-foreground min-h-screen p-6'>
      <div className='w-full max-w-[663px] space-y-6'>
        <Case label='A · 空态（看 placeholder 与图标）' code='' />
        <Case
          label='B · 填入兑换码后（看文字会不会被图标压住）'
          code='Po1nt9-TM-2026-ABCD'
        />
      </div>
    </div>
  </StrictMode>
)
