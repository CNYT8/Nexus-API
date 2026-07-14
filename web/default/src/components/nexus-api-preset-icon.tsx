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
import { useTranslation } from 'react-i18next'

export const NEXUS_HK_CLOUDFLARE_PRESET = 'nexus-hk-cloudflare'
export const NEXUS_HK_CLOUDFLARE_LABEL =
  'Nexus-API preset | Hong Kong + Cloudflare'

export function isNexusHongKongCloudflarePreset(color?: string) {
  return color === NEXUS_HK_CLOUDFLARE_PRESET
}

function sanitizeId(id: string) {
  return id.replace(/[^a-zA-Z0-9_-]/g, '')
}

export function NexusApiPresetIcon(props: { size?: number }) {
  const { t } = useTranslation()
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
        aria-label={t(NEXUS_HK_CLOUDFLARE_LABEL)}
      >
        <defs>
          <linearGradient id={frameId} x1='0' y1='0' x2='1' y2='1'>
            <stop offset='0%' stopColor='#de2910' />
            <stop offset='52%' stopColor='#f97316' />
            <stop offset='100%' stopColor='#fbbf24' />
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
          <rect x='4.5' y='5' width='25.5' height='17' rx='3.5' fill='#de2910' />
          <g transform='translate(17.25 13.5) scale(0.78)'>
            {[0, 72, 144, 216, 288].map((angle) => (
              <g key={angle} transform={`rotate(${angle})`}>
                <path
                  d='M0 -8.5 C3 -7.4 4.3 -4.7 2.9 -2.3 C1.7 -0.4 0.4 0.3 -1.6 -0.5 C-3.5 -1.3 -3.7 -5.2 0 -8.5Z'
                  fill='white'
                />
                <circle cx='1.45' cy='-5.1' r='0.7' fill='#de2910' />
                <path
                  d='M0.55 -7.1 C1.8 -6 2.2 -4.6 1.7 -3'
                  fill='none'
                  stroke='#de2910'
                  strokeLinecap='round'
                  strokeWidth='0.5'
                />
              </g>
            ))}
            <circle cx='0' cy='0' r='0.9' fill='#de2910' />
          </g>
          <path
            d='M20.2 30.2h10.5c3 0 5.4-1.9 5.4-4.3 0-2.2-1.9-3.9-4.5-4.2-.9-2.7-3.3-4.6-6.2-4.6-2.6 0-4.8 1.5-5.8 3.8-2.3.3-4.2 2.1-4.2 4.3 0 2.8 2 5 4.8 5Z'
            fill={`url(#${cloudId})`}
          />
          <path
            d='M21.6 27.8h8.8c1.9 0 3.4-1.1 3.5-2.5.1-.7-.5-1.4-1.3-1.6-.7-.1-1.5.1-2.1.5-1-.8-2.4-1.2-3.8-.9-1.3.3-2.3 1-2.8 2.1-1.3-.5-3-.2-3.9.7-.8.7-.2 1.7 1.6 1.7Z'
            fill='#fff7ed'
            opacity='0.8'
          />
        </g>
      </svg>
    </span>
  )
}
