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
import BaichuanColor from '@lobehub/icons/es/Baichuan/components/Color.js'
import ClaudeColor from '@lobehub/icons/es/Claude/components/Color.js'
import CohereColor from '@lobehub/icons/es/Cohere/components/Color.js'
import DeepSeekColor from '@lobehub/icons/es/DeepSeek/components/Color.js'
import DoubaoColor from '@lobehub/icons/es/Doubao/components/Color.js'
import FluxMono from '@lobehub/icons/es/Flux/components/Mono.js'
import GeminiColor from '@lobehub/icons/es/Gemini/components/Color.js'
import GrokMono from '@lobehub/icons/es/Grok/components/Mono.js'
import HailuoColor from '@lobehub/icons/es/Hailuo/components/Color.js'
import HunyuanColor from '@lobehub/icons/es/Hunyuan/components/Color.js'
import JimengColor from '@lobehub/icons/es/Jimeng/components/Color.js'
import KimiMono from '@lobehub/icons/es/Kimi/components/Mono.js'
import KlingColor from '@lobehub/icons/es/Kling/components/Color.js'
import LumaColor from '@lobehub/icons/es/Luma/components/Color.js'
import MetaColor from '@lobehub/icons/es/Meta/components/Color.js'
import MidjourneyMono from '@lobehub/icons/es/Midjourney/components/Mono.js'
import MinimaxColor from '@lobehub/icons/es/Minimax/components/Color.js'
import MistralColor from '@lobehub/icons/es/Mistral/components/Color.js'
import OpenAIMono from '@lobehub/icons/es/OpenAI/components/Mono.js'
import PerplexityColor from '@lobehub/icons/es/Perplexity/components/Color.js'
import QwenColor from '@lobehub/icons/es/Qwen/components/Color.js'
import RunwayMono from '@lobehub/icons/es/Runway/components/Mono.js'
import SoraColor from '@lobehub/icons/es/Sora/components/Color.js'
import SparkColor from '@lobehub/icons/es/Spark/components/Color.js'
import StabilityColor from '@lobehub/icons/es/Stability/components/Color.js'
import StepfunMono from '@lobehub/icons/es/Stepfun/components/Mono.js'
import SunoMono from '@lobehub/icons/es/Suno/components/Mono.js'
import ViduColor from '@lobehub/icons/es/Vidu/components/Color.js'
import WenxinColor from '@lobehub/icons/es/Wenxin/components/Color.js'
import XiaomiMiMoMono from '@lobehub/icons/es/XiaomiMiMo/components/Mono.js'
import YiColor from '@lobehub/icons/es/Yi/components/Color.js'
import ZhipuColor from '@lobehub/icons/es/Zhipu/components/Color.js'

/** Icon components shipped by @lobehub/icons (size in px, optional style). */
export type VendorIcon = typeof ClaudeColor

/**
 * One model vendor on the home page logo wall. Names are proper nouns and are
 * not translated; `zh` is the name Chinese readers know the product by.
 */
export type ModelVendor = {
  id: string
  en: string
  zh: string
  Icon: VendorIcon
}

/**
 * Model vendors shown on the home page logo wall (user decision, 2026-11-25:
 * list every major AI vendor). Which models a site actually offers, and at
 * what price, is the pricing page's job; the wall links there.
 */
export const MODEL_VENDORS: ModelVendor[] = [
  { id: 'openai', en: 'OpenAI', zh: 'OpenAI', Icon: OpenAIMono },
  { id: 'claude', en: 'Claude', zh: 'Claude', Icon: ClaudeColor },
  { id: 'gemini', en: 'Gemini', zh: 'Gemini', Icon: GeminiColor },
  { id: 'deepseek', en: 'DeepSeek', zh: 'DeepSeek', Icon: DeepSeekColor },
  { id: 'grok', en: 'Grok', zh: 'Grok', Icon: GrokMono },
  { id: 'qwen', en: 'Qwen', zh: '通义千问', Icon: QwenColor },
  { id: 'doubao', en: 'Doubao', zh: '豆包', Icon: DoubaoColor },
  { id: 'kimi', en: 'Kimi', zh: 'Kimi', Icon: KimiMono },
  { id: 'zhipu', en: 'Zhipu GLM', zh: '智谱 GLM', Icon: ZhipuColor },
  { id: 'minimax', en: 'MiniMax', zh: 'MiniMax', Icon: MinimaxColor },
  { id: 'mistral', en: 'Mistral', zh: 'Mistral', Icon: MistralColor },
  { id: 'meta', en: 'Meta Llama', zh: 'Meta Llama', Icon: MetaColor },
  { id: 'xiaomi', en: 'Xiaomi MiMo', zh: '小米 MiMo', Icon: XiaomiMiMoMono },
  { id: 'hunyuan', en: 'Hunyuan', zh: '腾讯混元', Icon: HunyuanColor },
  { id: 'wenxin', en: 'ERNIE', zh: '文心', Icon: WenxinColor },
  { id: 'spark', en: 'iFlytek Spark', zh: '讯飞星火', Icon: SparkColor },
  { id: 'stepfun', en: 'StepFun', zh: '阶跃星辰', Icon: StepfunMono },
  { id: 'baichuan', en: 'Baichuan', zh: '百川', Icon: BaichuanColor },
  { id: 'yi', en: '01.AI Yi', zh: '零一万物', Icon: YiColor },
  { id: 'cohere', en: 'Cohere', zh: 'Cohere', Icon: CohereColor },
  {
    id: 'perplexity',
    en: 'Perplexity',
    zh: 'Perplexity',
    Icon: PerplexityColor,
  },
  { id: 'sora', en: 'Sora', zh: 'Sora', Icon: SoraColor },
  { id: 'kling', en: 'Kling', zh: '可灵', Icon: KlingColor },
  { id: 'jimeng', en: 'Jimeng', zh: '即梦', Icon: JimengColor },
  { id: 'hailuo', en: 'Hailuo', zh: '海螺', Icon: HailuoColor },
  { id: 'vidu', en: 'Vidu', zh: 'Vidu', Icon: ViduColor },
  {
    id: 'midjourney',
    en: 'Midjourney',
    zh: 'Midjourney',
    Icon: MidjourneyMono,
  },
  { id: 'flux', en: 'FLUX', zh: 'FLUX', Icon: FluxMono },
  {
    id: 'stability',
    en: 'Stability AI',
    zh: 'Stability AI',
    Icon: StabilityColor,
  },
  { id: 'runway', en: 'Runway', zh: 'Runway', Icon: RunwayMono },
  { id: 'luma', en: 'Luma', zh: 'Luma', Icon: LumaColor },
  { id: 'suno', en: 'Suno', zh: 'Suno', Icon: SunoMono },
]
