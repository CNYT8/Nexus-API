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
        'border-border/30 bg-background/60 supports-[backdrop-filter]:bg-background/50 sticky top-0 z-40 isolate h-[var(--app-header-height,3rem)] w-full shrink-0 overflow-hidden border-b shadow-[inset_0_1px_0_rgba(255,255,255,0.38),0_12px_32px_-30px_rgba(15,23,42,0.75)] backdrop-blur-xl backdrop-saturate-150 before:pointer-events-none before:absolute before:inset-x-0 before:top-0 before:h-px before:bg-white/40 after:pointer-events-none after:absolute after:inset-x-3 after:bottom-0 after:h-px after:bg-gradient-to-r after:from-transparent after:via-foreground/10 after:to-transparent dark:border-white/10 dark:bg-background/50 dark:shadow-[inset_0_1px_0_rgba(255,255,255,0.08),0_12px_32px_-30px_rgba(0,0,0,0.95)] dark:before:bg-white/10',
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
      {logoAccent.active && (
        <div
          aria-hidden='true'
          className='pointer-events-none absolute inset-0 opacity-75 mix-blend-normal'
          style={{
            background:
              'radial-gradient(24rem 8rem at 2.5rem -2rem, rgba(var(--header-logo-accent), 0.22), transparent 72%), radial-gradient(18rem 6rem at 42% -2.5rem, rgba(var(--header-logo-accent), 0.08), transparent 74%)',
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
