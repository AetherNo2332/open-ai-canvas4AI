import type { Metadata } from 'next';
import { Providers } from '@/components/providers';

import './global.css';

export const metadata: Metadata = {
  title: {
    default: '影策文档',
    template: '%s | 影策文档',
  },
  description: '影策（4AI）开发、运维与验收文档',
};

export default function Layout({ children }: LayoutProps<'/'>) {
  return (
    <html lang="zh-CN" suppressHydrationWarning>
      <body className="flex min-h-screen flex-col">
        <Providers>{children}</Providers>
      </body>
    </html>
  );
}
