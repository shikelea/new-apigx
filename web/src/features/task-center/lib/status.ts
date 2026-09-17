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
*/// 任务状态的文案映射。
//
// 后端的词表是**厂商词表**（NOT_START / SUBMITTED / QUEUED / IN_PROGRESS / SUCCESS / FAILURE，
// 见 `model.TaskStatus*`）—— 阶段 3 修复 kling 插件时确认过这一点 ✓。
// 未知取值**回退为原值**，让新状态在界面上一眼可见，而不是静默显示空白或错误文案。

const STATUS_LABEL_KEYS: Record<string, string> = {
  NOT_START: 'taskCenter.status.notStart',
  SUBMITTED: 'taskCenter.status.submitted',
  QUEUED: 'taskCenter.status.queued',
  IN_PROGRESS: 'taskCenter.status.inProgress',
  SUCCESS: 'taskCenter.status.success',
  FAILURE: 'taskCenter.status.failure',
}

/** 状态 → i18n 键；未知状态返回空串（由调用方显示原值）。 */
export function taskStatusLabelKey(status: string): string {
  return STATUS_LABEL_KEYS[status] ?? ''
}

/** 任务是否已到终态（终态才需要展示结果或失败原因）。 */
export function isTaskTerminal(status: string): boolean {
  return status === 'SUCCESS' || status === 'FAILURE'
}