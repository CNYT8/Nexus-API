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
const MAX_MICROS = '9223372036854775807';

// Accept at most six decimal places; reject excess precision, never round.
export function moneyToMicros(value) {
  const match = /^(\d+)(?:\.(\d{0,6}))?$/.exec(value.trim());
  if (!match) return null;
  const micros = `${match[1]}${(match[2] ?? '').padEnd(6, '0')}`.replace(
    /^0+(?=\d)/,
    '',
  );
  if (
    micros.length > MAX_MICROS.length ||
    (micros.length === MAX_MICROS.length && micros > MAX_MICROS)
  ) {
    return null;
  }
  return micros;
}

export function microsToMoney(value) {
  const digits = value.replace(/^0+(?=\d)/, '').padStart(7, '0');
  const fraction = digits.slice(-6).replace(/0+$/, '');
  return `${digits.slice(0, -6)}${fraction ? `.${fraction}` : ''}`;
}
