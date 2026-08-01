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

import LiquidGlass from 'liquid-glass-react';
import { getLiquidGlassHeaderProps } from '../../../../../liquid-glass-header-config.js';

const LiquidGlassHeader = ({ overLight, cornerRadius = 0, className = '' }) => (
  <div
    aria-hidden='true'
    className={`nexus-liquid-glass-layer pointer-events-none absolute inset-0 z-0 ${className}`}
  >
    <LiquidGlass
      {...getLiquidGlassHeaderProps({ overLight, cornerRadius })}
      className='nexus-liquid-glass-surface'
      style={{
        position: 'absolute',
        top: 0,
        left: 0,
        width: '100%',
        height: '100%',
        pointerEvents: 'none',
      }}
    />
  </div>
);

export default LiquidGlassHeader;
