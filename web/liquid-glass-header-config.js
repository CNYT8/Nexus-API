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

const STATIC_MOUSE_POSITION = Object.freeze({ x: 0, y: 0 });

const LIQUID_GLASS_HEADER_CONFIG = Object.freeze({
  displacementScale: 14,
  saturation: 145,
  aberrationIntensity: 0.35,
  elasticity: 0,
  padding: '0',
  mode: 'standard',
});

export const getLiquidGlassHeaderProps = ({
  overLight = false,
  cornerRadius = 0,
} = {}) => ({
  ...LIQUID_GLASS_HEADER_CONFIG,
  blurAmount: overLight ? 0.125 : 0.375,
  cornerRadius,
  overLight,
  globalMousePos: STATIC_MOUSE_POSITION,
  mouseOffset: STATIC_MOUSE_POSITION,
});
