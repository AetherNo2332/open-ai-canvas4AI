'use client';

import Link from 'next/link';
import { useParams, usePathname, useRouter } from 'next/navigation';
import { RootProvider } from 'fumadocs-ui/provider/base';
import { FrameworkProvider, type Framework } from 'fumadocs-core/framework';
import type { ComponentProps, ReactNode } from 'react';
import { uiTranslations } from '@/lib/i18n';

// Content/search URLs include the public prefix; Next Link applies basePath itself.
function withoutBasePath(href: string) {
  return href === '/docs' ? '/' : href.startsWith('/docs/') ? href.slice(5) : href;
}

function DocsLink({ href = '', ...props }: ComponentProps<NonNullable<Framework['Link']>>) {
  return <Link href={withoutBasePath(href)} {...props} />;
}

function useDocsPathname() {
  const path = usePathname();
  return '/docs' + (path === '/' ? '' : path);
}

function useDocsRouter() {
  const router = useRouter();
  return { push: (url: string) => router.push(withoutBasePath(url)), refresh: () => router.refresh() };
}

export function Providers({ children }: { children: ReactNode }) {
  return (
    <FrameworkProvider Link={DocsLink} usePathname={useDocsPathname} useRouter={useDocsRouter} useParams={useParams}>
    <RootProvider
      search={{ options: { api: '/docs/api/search' } }}
      i18n={{ locale: 'cn', translations: uiTranslations }}
    >
      {children}
    </RootProvider>
    </FrameworkProvider>
  );
}
