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

import { describe, expect, it } from 'vitest'

import i18n from '@/i18n/config'
import en from '@/i18n/locales/en.json'
import zh from '@/i18n/locales/zh.json'

// 回归用例：界面上曾显示 `usage.title` 这类**原始键名**。
//
// 真实原因不是缓存也不是构建，而是**语言文件的层级放错了**：新增的键被写到 JSON 顶层，
// 而 i18next 的资源结构是 `{ 语言: { 命名空间: { 键: 文案 } } }` —— 顶层散键会被当成
// **命名空间名**，`translation` 里查不到，于是 `t()` 原样回显键名。
// 表现之所以"只有少数地方露馅"，是因为本项目的键**就是英文原文**：查不到时回显的英文看着正常。
//
// 第一个 describe 守住**数据契约**（真正的缺陷所在）；
// 第二个 describe 显式灌入资源后断言**解析结果**，避免受测试环境 JSON 导入互操作差异影响。
describe('i18n 语言文件结构（数据契约）', () => {
  it('每个语言文件顶层只应有 translation 命名空间', () => {
    expect(Object.keys(en)).toEqual(['translation'])
    expect(Object.keys(zh)).toEqual(['translation'])
  })

  it('带点的键位于 translation 命名空间内（而不是顶层）', () => {
    const enNs = en.translation as Record<string, string>
    const zhNs = zh.translation as Record<string, string>
    for (const key of ['usage.title', 'usage.model', 'taskCenter.title']) {
      expect(enNs[key], `en 缺少 ${key}`).toBeTruthy()
      expect(zhNs[key], `zh 缺少 ${key}`).toBeTruthy()
    }
  })
})

describe('i18n 运行时解析', () => {
  it('带点的键解析为译文，而不是回显键名', async () => {
    i18n.addResourceBundle('zhCN', 'translation', zh.translation, true, true)
    i18n.addResourceBundle('en', 'translation', en.translation, true, true)

    await i18n.changeLanguage('zhCN')
    expect(i18n.t('usage.title')).toBe('我的用量')

    await i18n.changeLanguage('en')
    const rawKeys = ['usage.title', 'usage.model', 'taskCenter.title'].filter(
      (key) => i18n.t(key) === key
    )
    expect(rawKeys).toEqual([])
  })
})
