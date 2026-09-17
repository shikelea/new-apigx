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
*/// 保留期限的计算。
//
// 语义（与后端策略一致）：保留期限是**本平台的承诺期限**，从任务**完成时刻**起算。
// 上游自己签发的直链可能更早失效 —— 那是上游行为，不在本函数里猜测。

/** 保留到期的 UTC 秒；`days <= 0` 或没有完成时刻时返回 null（= 不作承诺，界面留空）。 */
export function retentionDeadlineUnix(finishTime: number | undefined, days: number): number | null {
  if (!Number.isFinite(days) || days <= 0) {
    return null
  }
  if (finishTime === undefined || !Number.isFinite(finishTime) || finishTime <= 0) {
    return null
  }
  return finishTime + days * 24 * 60 * 60
}