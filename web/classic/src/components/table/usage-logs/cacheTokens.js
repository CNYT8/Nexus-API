/*
Copyright (C) 2025 QuantumNous

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

function toTokenCount(value) {
  const count = Number(value);
  return Number.isFinite(count) && count > 0 ? count : 0;
}

export function getCacheWriteBreakdown(other) {
  const fiveMinutes = toTokenCount(other?.cache_creation_tokens_5m);
  const oneHour = toTokenCount(other?.cache_creation_tokens_1h);
  const twelveHours = toTokenCount(other?.cache_creation_tokens_12h);
  const splitTotal = fiveMinutes + oneHour + twelveHours;
  // The aggregate already includes TTL buckets; only its remainder is ordinary.
  const ordinary = Math.max(
    toTokenCount(other?.cache_creation_tokens) - splitTotal,
    0,
  );

  return {
    ordinary,
    fiveMinutes,
    oneHour,
    twelveHours,
    total: ordinary + splitTotal,
  };
}
