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
import { useApiBaseUrl } from '@/features/adty-site/api'
import { useAuthStore } from '@/stores/auth-store'

import { AffiliateBanner } from './components/affiliate-banner'
import { BillingSection } from './components/billing-section'
import { CapabilityBento } from './components/capability-bento'
import { FaqSection } from './components/faq-section'
import { FeatureCards } from './components/feature-cards'
import { Hero } from './components/hero'
import { ModelWall } from './components/model-wall'
import { QuickStart } from './components/quick-start'
import { useLandingData, type LandingData } from './hooks/use-landing-data'

/**
 * The landing page blocks from the hero down, built only from props. The
 * build-time prerender (task 9.3) renders this same component without data,
 * so nothing here may touch window, document or localStorage, and links are
 * plain anchors.
 */
export function LandingSections(props: {
  signedIn: boolean
  baseUrl: string
  data: LandingData
}) {
  // Before the site address loads (or when it cannot), show the same
  // placeholder the build-time render uses instead of a bare "/v1".
  const baseUrl = props.baseUrl.trim() || '<BASE_URL>'
  return (
    <>
      <Hero signedIn={props.signedIn} />
      <ModelWall />
      <FeatureCards baseUrl={baseUrl} />
      <BillingSection />
      <CapabilityBento />
      <FaqSection />
      <AffiliateBanner
        open={props.data.affiliate.open}
        rate={props.data.affiliate.rate}
      />
      <QuickStart signedIn={props.signedIn} baseUrl={baseUrl} />
    </>
  )
}

/**
 * Default home page (adty task 8.3); the public layout adds header and
 * footer.
 */
export function AdtyLanding() {
  const user = useAuthStore((state) => state.auth.user)
  const baseUrl = useApiBaseUrl()
  const data = useLandingData()
  return <LandingSections signedIn={!!user} baseUrl={baseUrl} data={data} />
}
