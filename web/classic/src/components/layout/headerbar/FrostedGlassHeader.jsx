/*
Copyright (C) 2026 QuantumNous

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

const FrostedGlassHeader = ({ cornerRadius = 0, className = '' }) => (
  <div
    aria-hidden='true'
    className={`nexus-frosted-glass-layer pointer-events-none absolute inset-0 z-0 ${className}`}
    style={{ borderRadius: `${cornerRadius}px` }}
  />
);

export default FrostedGlassHeader;
