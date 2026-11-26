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

import { useRoute } from 'vitepress'
import type { Theme } from 'vitepress'
import DefaultTheme from 'vitepress/theme'
import { nextTick, onMounted, watch } from 'vue'

import { applyBrandToDOM, resolveSiteBrand, type SiteBrand } from './brand'
import MainSiteOnly from './components/MainSiteOnly.vue'
import PaymentMethodsList from './components/PaymentMethodsList.vue'
import ReferralSummary from './components/ReferralSummary.vue'
import SetupWizard from './components/SetupWizard.vue'
import './style.css'

export default {
  extends: DefaultTheme,
  enhanceApp({ app }) {
    app.component('SetupWizard', SetupWizard)
    app.component('ReferralSummary', ReferralSummary)
    app.component('PaymentMethodsList', PaymentMethodsList)
    app.component('MainSiteOnly', MainSiteOnly)
  },
  setup() {
    const route = useRoute()
    let activeBrand: SiteBrand | null = null

    const runApply = () => {
      if (!activeBrand) return
      void nextTick(() => {
        if (!activeBrand) return
        applyBrandToDOM(activeBrand)
        window.setTimeout(() => {
          if (activeBrand) applyBrandToDOM(activeBrand)
        }, 120)
      })
    }

    onMounted(async () => {
      activeBrand = await resolveSiteBrand()
      runApply()
    })

    watch(
      () => route.path,
      () => {
        if (activeBrand) {
          runApply()
        }
      }
    )
  },
} satisfies Theme
