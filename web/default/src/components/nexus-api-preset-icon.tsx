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
import { useId } from 'react'

export const NEXUS_HK_CLOUDFLARE_PRESET = 'nexus-hk-cloudflare'
export const NEXUS_HK_CLOUDFLARE_LABEL = 'Nexus-API预设｜🇭🇰香港+Cloudflare'

export function isNexusHongKongCloudflarePreset(color?: string) {
  return color === NEXUS_HK_CLOUDFLARE_PRESET
}

function sanitizeId(id: string) {
  return id.replace(/[^a-zA-Z0-9_-]/g, '')
}

export function NexusApiPresetIcon(props: { size?: number }) {
  const size = props.size ?? 24
  const svgId = sanitizeId(useId())
  const frameId = `nexus-hk-cf-frame-${svgId}`
  const cloudId = `nexus-hk-cf-cloud-${svgId}`
  const shadowId = `nexus-hk-cf-shadow-${svgId}`

  return (
    <span
      className='inline-flex shrink-0 items-center justify-center'
      style={{ width: size, height: size }}
    >
      <svg
        width={size}
        height={size}
        viewBox='0 0 40 40'
        role='img'
        aria-label={NEXUS_HK_CLOUDFLARE_LABEL}
      >
        <defs>
          <linearGradient id={frameId} x1='0' y1='0' x2='1' y2='1'>
            <stop offset='0%' stopColor='#111827' />
            <stop offset='100%' stopColor='#334155' />
          </linearGradient>
          <linearGradient id={cloudId} x1='0' y1='0' x2='1' y2='1'>
            <stop offset='0%' stopColor='#fbbf24' />
            <stop offset='42%' stopColor='#f97316' />
            <stop offset='100%' stopColor='#ea580c' />
          </linearGradient>
          <filter id={shadowId} x='-20%' y='-20%' width='140%' height='140%'>
            <feDropShadow
              dx='0'
              dy='1.4'
              stdDeviation='1.2'
              floodOpacity='0.22'
            />
          </filter>
        </defs>
        <rect
          x='1.5'
          y='1.5'
          width='37'
          height='37'
          rx='10'
          fill='white'
          stroke={`url(#${frameId})`}
          strokeWidth='2'
        />
        <g filter={`url(#${shadowId})`}>
          <rect x='5' y='7' width='19' height='19' rx='5' fill='#de2910' />
          <g transform='translate(14.5 16.5)'>
            {[0, 72, 144, 216, 288].map((angle) => (
              <path
                key={angle}
                d='M0 -7 C2.4 -6 3.6 -3.6 2.2 -1.5 C1.2 0 0.1 0.4 -1.6 -0.5 C-3.1 -1.4 -3 -4.4 0 -7Z'
                fill='white'
                transform={`rotate(${angle})`}
              />
            ))}
            <circle cx='0' cy='0' r='1.2' fill='#de2910' />
          </g>
          <path
            d='M17.2 26.2h12.9c3.1 0 5.7-2.1 5.7-4.8 0-2.4-2-4.4-4.8-4.7-.9-3.2-3.7-5.4-7.1-5.4-3.1 0-5.8 1.9-6.9 4.7-2.7.4-4.8 2.5-4.8 5 0 2.9 2.2 5.2 5 5.2Z'
            fill={`url(#${cloudId})`}
          />
          <path
            d='M19.1 24.2h11.1c2 0 3.6-1.2 3.8-2.9.1-.8-.5-1.6-1.4-1.8-.8-.2-1.6.1-2.3.6-1.1-1-2.7-1.5-4.3-1.1-1.5.3-2.6 1.2-3.2 2.4-1.5-.6-3.4-.2-4.5.8-.8.8-.4 2 .8 2Z'
            fill='#fff7ed'
            opacity='0.8'
          />
        </g>
      </svg>
    </span>
  )
}
