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

const LIQUID_GLASS_HEADER_CONFIG = Object.freeze({
  saturation: 148,
  aberrationIntensity: 2,
  // Keep the full-width header fixed while allowing the oversized optical
  // surface to respond subtly to the pointer.
  elasticity: 0.055,
  padding: '0',
  mode: 'standard',
});

export const getLiquidGlassHeaderProps = ({
  overLight = false,
  cornerRadius = 0,
} = {}) => ({
  ...LIQUID_GLASS_HEADER_CONFIG,
  // liquid-glass-react halves displacement over light surfaces. Keep a
  // clearly visible, equal refraction strength across themes.
  displacementScale: overLight ? 96 : 48,
  blurAmount: 0.0625,
  cornerRadius,
  overLight,
});
