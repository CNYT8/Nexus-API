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
import { SidebarTrigger } from '@/components/ui/sidebar'

type HeaderProps = React.HTMLAttributes<HTMLElement>

export function Header({ className, children, style, ...props }: HeaderProps) {
  const { logo } = useSystemConfig()
  const logoAccent = useLogoAccent(logo)

  return (
    <header
      className={cn(
        'border-transparent bg-background/60 supports-[backdrop-filter]:bg-background/50 sticky top-0 z-40 isolate h-[var(--app-header-height,3rem)] w-full shrink-0 overflow-hidden border-b shadow-[inset_0_1px_0_rgba(255,255,255,0.52),inset_0_-1px_0_rgba(255,255,255,0.16),0_18px_42px_-32px_rgba(15,23,42,0.78)] backdrop-blur-2xl backdrop-brightness-105 backdrop-contrast-125 backdrop-saturate-200 before:pointer-events-none before:absolute before:inset-x-0 before:top-0 before:h-px before:bg-white/60 after:pointer-events-none after:absolute after:inset-x-3 after:bottom-0 after:h-px after:bg-gradient-to-r after:from-transparent after:via-white/30 after:to-transparent dark:bg-background/50 dark:shadow-[inset_0_1px_0_rgba(255,255,255,0.12),inset_0_-1px_0_rgba(255,255,255,0.06),0_18px_42px_-32px_rgba(0,0,0,0.98)] dark:before:bg-white/15 dark:after:via-white/15',
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
      <div
        aria-hidden='true'
        className='pointer-events-none absolute inset-0 z-0 opacity-95'
        style={{
          background:
            'linear-gradient(115deg, rgba(255,255,255,0.34) 0%, rgba(255,255,255,0.12) 28%, rgba(255,255,255,0.03) 52%, rgba(255,255,255,0.18) 100%), radial-gradient(30rem 10rem at 12% -5rem, rgba(255,255,255,0.34), transparent 72%), radial-gradient(18rem 7rem at 88% -3rem, rgba(255,255,255,0.16), transparent 70%)',
        }}
      />
      {logoAccent.active && (
        <div
          aria-hidden='true'
          className='pointer-events-none absolute inset-0 z-0 opacity-90 mix-blend-normal'
          style={{
            background:
              'radial-gradient(26rem 9rem at 2.5rem -2rem, rgba(var(--header-logo-accent), 0.28), transparent 72%), radial-gradient(20rem 7rem at 42% -2.5rem, rgba(var(--header-logo-accent), 0.12), transparent 74%)',
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
