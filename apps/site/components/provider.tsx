'use client';
import SearchDialog from '@/components/search';
import { RootProvider } from 'fumadocs-ui/provider/next';
import { usePathname } from 'next/navigation';
import { type ReactNode } from 'react';

export function Provider({ children }: { children: ReactNode }) {
  // The docs follow the system theme. Everything else (the landing, the
  // legal pages, the 404) is dark only, so the search dialog, the scrollbar
  // and color-scheme match the page.
  const pathname = usePathname();
  const forcedTheme = pathname.startsWith('/docs') ? undefined : 'dark';
  return (
    <RootProvider search={{ SearchDialog }} theme={{ forcedTheme }}>
      {children}
    </RootProvider>
  );
}
