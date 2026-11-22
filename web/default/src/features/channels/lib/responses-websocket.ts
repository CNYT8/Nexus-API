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

// Keep capability gating aligned with ChannelSupportsResponsesWebSocket and MatchPath.
// Enabled status and the explicit opt-in are enforced by the relay at runtime.
export function supportsResponsesWebSocket(
  type: number,
  advancedCustom?: string
): boolean {
  if ([1, 3, 57].includes(type)) return true
  if (type !== 58) return false
  try {
    const config = JSON.parse(advancedCustom || '{}')
    if (!Array.isArray(config?.advanced_routes)) return false
    const route = config.advanced_routes.find(
      (entry: { incoming_path?: unknown } | null) => {
        if (typeof entry?.incoming_path !== 'string') return false
        const path = entry.incoming_path.trim()
        const target = '/v1/responses'
        if (!path.includes('{model}')) return path === target
        const parts = path.split('{model}')
        if (
          parts.length !== 2 ||
          !target.startsWith(parts[0]) ||
          !target.endsWith(parts[1])
        ) {
          return false
        }
        // Match Go's sequential TrimPrefix and TrimSuffix, including overlaps.
        let model = target.slice(parts[0].length)
        if (parts[1] && model.endsWith(parts[1])) {
          model = model.slice(0, -parts[1].length)
        }
        return model.length > 0 && !model.includes('/')
      }
    )
    const converter = route?.converter
    return (
      !!route &&
      (converter == null ||
        (typeof converter === 'string' &&
          ['', 'none'].includes(converter.trim())))
    )
  } catch {
    return false
  }
}
