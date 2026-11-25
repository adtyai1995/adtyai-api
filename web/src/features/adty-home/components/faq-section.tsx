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
import { useTranslation } from 'react-i18next'

import {
  Accordion,
  AccordionContent,
  AccordionItem,
  AccordionTrigger,
} from '@/components/ui/accordion'
import { cn } from '@/lib/utils'

import { HOME_CONTAINER, HOME_SECTION_TITLE } from './home-styles'

type FaqEntry = { question: string; answer: string }

function FaqColumn(props: { entries: FaqEntry[] }) {
  return (
    <Accordion className='gap-4'>
      {props.entries.map((entry) => (
        <AccordionItem
          key={entry.question}
          value={entry.question}
          className='bg-muted rounded-2xl not-last:border-b-0'
        >
          <AccordionTrigger className='**:data-[slot=accordion-trigger-icon]:text-primary px-7 py-6 text-[16px] font-medium hover:no-underline **:data-[slot=accordion-trigger-icon]:size-5 md:px-9'>
            {entry.question}
          </AccordionTrigger>
          <AccordionContent className='text-muted-foreground px-7 pb-6 text-[15px] leading-[1.8] md:px-9'>
            <p>{entry.answer}</p>
          </AccordionContent>
        </AccordionItem>
      ))}
    </Accordion>
  )
}

/**
 * Two columns of frequently asked questions, taken from the FAQ document so
 * the home page and the docs never disagree.
 */
export function FaqSection() {
  const { t } = useTranslation()
  const entries: FaqEntry[] = [
    {
      question: t('How am I billed?'),
      answer: t(
        'Pay as you go. Each request is charged for the tokens it uses (or per call or per image for some models), at the prices on the Pricing page. Your usage logs show the cost of every request. There is no monthly fee.'
      ),
    },
    {
      question: t('How do I migrate existing code?'),
      answer: t(
        'Keep your SDK and change its base URL to this site’s API address. Anthropic and Gemini clients keep sending their own auth headers.'
      ),
    },
    {
      question: t('How do I top up?'),
      answer: t(
        'Open Wallet, choose an amount and a payment method (USDT, Alipay or WeChat Pay, depending on what is enabled), and pay in the window that opens. The balance is credited automatically once the payment is confirmed. The minimum amount and any top-up bonus are shown on the Wallet page.'
      ),
    },
    {
      question: t('My payment went through but the balance did not change'),
      answer: t(
        'USDT payments are credited after the network confirms the transfer, which can take a few minutes. Alipay and WeChat Pay are usually credited within a minute. If the balance has still not changed after 30 minutes, contact support with the order number shown in Wallet.'
      ),
    },
    {
      question: t('Which models can I use?'),
      answer: t(
        'Every model on the Pricing page. Use the model name exactly as shown there.'
      ),
    },
    {
      question: t('Can I use one key in several tools?'),
      answer: t(
        'Yes, but it is easier to create one key per tool on the API Keys page. Then you can see each tool’s usage separately and disable one without affecting the others.'
      ),
    },
    {
      question: t('Is my data stored?'),
      answer: t(
        'Your requests are passed to the model provider to generate the response. The usage logs in the console record the model, tokens and cost of each request. What is kept and for how long is described in the Privacy Policy.'
      ),
    },
    {
      question: t('Do you issue invoices?'),
      answer: t(
        'No. We do not issue invoices or VAT receipts. The order history in Wallet and the usage logs are the records we provide.'
      ),
    },
  ]
  const half = Math.ceil(entries.length / 2)

  return (
    <section aria-labelledby='faq-title' className='py-24 md:py-32'>
      <div className={HOME_CONTAINER}>
        <div className='mx-auto max-w-2xl text-center'>
          <h2 id='faq-title' className={HOME_SECTION_TITLE}>
            {t('Frequently asked questions')}
          </h2>
          <p className='text-muted-foreground mt-4 text-[16px]'>
            {t('Billing, migration and payments, answered briefly.')}
          </p>
        </div>
        <div
          className={cn('mt-14 grid items-start gap-4 lg:grid-cols-2 lg:gap-6')}
        >
          <FaqColumn entries={entries.slice(0, half)} />
          <FaqColumn entries={entries.slice(half)} />
        </div>
      </div>
    </section>
  )
}
