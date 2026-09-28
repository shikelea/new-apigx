import type { IntelligenceInput } from '../api'

export type IntelligenceMode = 'candy' | 'pelican-api' | 'pelican-html'

export type IntelligenceFormValues = {
  mode: IntelligenceMode
  base_url: string
  api_key: string
  model: string
  html: string
  consent: boolean
}

export const INTELLIGENCE_HTML_MAX_BYTES = 2 * 1024 * 1024

// toInput 把表单值转成请求体。
// 关键约束：HTML 模式只送 HTML，绝不携带 Base URL 或 Key（后端也会二次校验互斥）。
export function toIntelligenceInput(
  values: IntelligenceFormValues
): IntelligenceInput {
  if (values.mode === 'pelican-html') {
    return { benchmark: 'pelican', html: values.html }
  }
  return {
    benchmark: values.mode === 'candy' ? 'candy' : 'pelican',
    base_url: values.base_url.trim(),
    api_key: values.api_key.trim(),
    model: values.model.trim() || undefined,
  }
}
