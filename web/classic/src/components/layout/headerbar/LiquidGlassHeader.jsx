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

import { useCallback, useEffect, useRef } from 'react';
import LiquidGlass from 'liquid-glass-react';
import { getLiquidGlassHeaderProps } from '../../../../../liquid-glass-header-config.js';

const LiquidGlassHeader = ({ overLight, cornerRadius = 0, className = '' }) => {
  const layerRef = useRef(null);
  const mouseContainerRef = useRef(null);
  const bindLayer = useCallback((node) => {
    layerRef.current = node;
    mouseContainerRef.current = node?.parentElement ?? null;
  }, []);

  useEffect(() => {
    const layer = layerRef.current;
    if (!layer) return undefined;

    // The package map is square and defaults to `slice`, which crops away the
    // refractive edges on a wide header. Re-fit it after package rerenders.
    let frameId = 0;
    const fitDisplacementMap = () => {
      layer
        .querySelectorAll('.nexus-liquid-glass-surface feImage')
        .forEach((image) => {
          if (image.getAttribute('preserveAspectRatio') !== 'none') {
            image.setAttribute('preserveAspectRatio', 'none');
          }
        });
    };
    const scheduleFit = () => {
      cancelAnimationFrame(frameId);
      frameId = requestAnimationFrame(fitDisplacementMap);
    };

    fitDisplacementMap();
    if (typeof MutationObserver === 'undefined') return undefined;

    const observer = new MutationObserver(scheduleFit);
    observer.observe(layer, {
      attributes: true,
      attributeFilter: ['href', 'preserveAspectRatio'],
      childList: true,
      subtree: true,
    });

    return () => {
      cancelAnimationFrame(frameId);
      observer.disconnect();
    };
  }, []);

  return (
    <div
      ref={bindLayer}
      aria-hidden='true'
      className={`nexus-liquid-glass-layer pointer-events-none absolute inset-0 z-0 ${className}`}
    >
      <LiquidGlass
        {...getLiquidGlassHeaderProps({ overLight, cornerRadius })}
        mouseContainer={mouseContainerRef}
        className='nexus-liquid-glass-surface'
        style={{
          position: 'absolute',
          top: 0,
          left: 0,
          width: '100%',
          height: '100%',
          pointerEvents: 'none',
        }}
      >
        {null}
      </LiquidGlass>
    </div>
  );
};

export default LiquidGlassHeader;
