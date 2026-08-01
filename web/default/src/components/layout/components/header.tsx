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
import { cn } from '@/lib/utils'
import { useSystemConfig } from '@/hooks/use-system-config'
import { useLogoAccent } from '@/hooks/use-logo-accent'
import { useTheme } from '@/context/theme-provider'
import { SidebarTrigger } from '@/components/ui/sidebar'
import { LiquidGlassHeader } from './liquid-glass-header'

type HeaderProps = React.HTMLAttributes<HTMLElement>

export function Header({ className, children, style, ...props }: HeaderProps) {
  const { logo } = useSystemConfig()
  const logoAccent = useLogoAccent(logo)
  const { resolvedTheme } = useTheme()

  return (
    <header
      className={cn(
        'border-transparent bg-transparent sticky top-0 z-40 isolate h-[var(--app-header-height,3rem)] w-full shrink-0 overflow-hidden border-b shadow-none',
        className
      )}
      style={{
        ...style,
        ...(logoAccent.active
          ? ({ '--header-logo-accent': logoAccent.rgb } as React.CSSProperties)
          : {}),
      }}
      {...props}
    >
      <LiquidGlassHeader overLight={resolvedTheme !== 'dark'} />
      {logoAccent.active && (
        <div
          aria-hidden='true'
          className='pointer-events-none absolute inset-0 z-0 opacity-[0.78] mix-blend-normal'
          style={{
            background:
              'radial-gradient(26rem 9rem at 2.5rem -2rem, rgba(var(--header-logo-accent), 0.22), transparent 72%), radial-gradient(20rem 7rem at 42% -2.5rem, rgba(var(--header-logo-accent), 0.09), transparent 74%)',
          }}
        />
      )}
      <div className='relative z-10 flex h-full items-center gap-1.5 px-2 sm:gap-2 sm:px-3'>
        <SidebarTrigger variant='ghost' className='size-8' />
        {children}
      </div>
    </header>
  )
}
