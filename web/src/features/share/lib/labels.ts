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
*/// 枚举 → i18n 键的映射。
//
// 放在纯函数里而不是散在组件中：这样"未知取值必须显示原值"这条约定可以被测试固定住 ——
// 否则后端新增枚举时界面会静默显示空白或错误文案（规范：枚举与文案统一约定）。

/** Key 池模式：0=轮询 / 1=随机；未知取值回退为原值字符串。 */
export function keyPoolModeLabelKey(mode: number): string {
  if (mode === 1) {
    return 'share.keyPool.random'
  }
  if (mode === 0) {
    return 'share.keyPool.polling'
  }
  return `share.keyPool.unknown:${mode}`
}

/** 默认可见性：0=公开 / 1=邀请 / 2=指定用户 / 3=仅自己。 */
export function visibilityLabelKey(visibility: number): string {
  const map: Record<number, string> = {
    0: 'share.visibility.public',
    1: 'share.visibility.invite',
    2: 'share.visibility.selected',
    3: 'share.visibility.private',
  }
  return map[visibility] ?? `share.visibility.unknown:${visibility}`
}

/** 渠道健康状态：0=正常 / 1=降级 / 2=不可用（含未知回退）。 */
export function healthLabelKey(state: number): string {
  const map: Record<number, string> = {
    0: 'share.health.healthy',
    1: 'share.health.degraded',
    2: 'share.health.unavailable',
  }
  return map[state] ?? `share.health.unknown:${state}`
}
/** 上游 Key 状态：0=启用 / 1=停用 / 2=自动禁用（未知回退保留原值）。 */
export function keyStatusLabelKey(status: number): string {
  const map: Record<number, string> = {
    0: 'share.keyStatus.enabled',
    1: 'share.keyStatus.disabled',
    2: 'share.keyStatus.autoDisabled',
  }
  return map[status] ?? `share.keyStatus.unknown:${status}`
}

/** 供给状态（未知回退保留原值）。 */
export function offeringStatusLabelKey(status: number): string {
  const map: Record<number, string> = {
    0: 'share.offeringStatus.draft',
    1: 'share.offeringStatus.submitted',
    2: 'share.offeringStatus.published',
    3: 'share.offeringStatus.rejected',
  }
  return map[status] ?? `share.offeringStatus.unknown:${status}`
}