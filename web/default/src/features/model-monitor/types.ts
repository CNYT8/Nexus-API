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
export type ModelMonitorStatus =
  | 'excellent'
  | 'good'
  | 'unstable'
  | 'poor'
  | 'unknown'

export interface ModelMonitorModel {
  model_name: string
  score: number
  status: ModelMonitorStatus
  status_text: string
  has_data: boolean
}

export interface ModelMonitorVendor {
  id: number
  name: string
  description?: string
  icon?: string
  score: number
  status: ModelMonitorStatus
  status_text: string
  known_count: number
  unknown_count: number
  models: ModelMonitorModel[]
}

export interface ModelMonitorSummary {
  window_days: number
  hot_days: number
  refresh_seconds: number
  updated_at: number
  model_count: number
  known_count: number
  unknown_count: number
  vendor_count: number
  best_score: number
  vendors: ModelMonitorVendor[]
}

export interface GetModelMonitorResponse {
  success: boolean
  message?: string
  data?: ModelMonitorSummary
}
