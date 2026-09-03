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

export interface ModelMonitorAvailabilityBucket {
  start: number
  end: number
  has_data: boolean
  score: number
  status: ModelMonitorStatus
}

export interface ModelMonitorAvailabilityModel {
  model_name: string
  buckets: ModelMonitorAvailabilityBucket[]
}

export interface ModelMonitorAvailabilityGroup {
  group: string
  buckets: ModelMonitorAvailabilityBucket[]
  models: ModelMonitorAvailabilityModel[]
}

export interface ModelMonitorAvailability {
  updated_at: number
  refresh_seconds: number
  bucket_seconds: number
  window_start: number
  window_end: number
  groups: ModelMonitorAvailabilityGroup[]
}
