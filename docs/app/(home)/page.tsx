import Link from 'next/link';

export default function HomePage() {
  return (
    <main className="mx-auto flex w-full max-w-3xl flex-1 flex-col justify-center gap-6 px-6 py-24">
      <p className="text-sm font-medium tracking-wide text-fd-muted-foreground">影策 · 4AI</p>
      <h1 className="text-4xl font-bold tracking-tight">影策文档</h1>
      <p className="text-lg text-fd-muted-foreground">
        面向开发、运维与验收的专题文档：后端 HTTP 合同、创作平台 SDK、画布与 Agent
        运行时、插件、部署与待测试边界。
      </p>
      <div className="flex flex-wrap gap-3">
        <Link
          href="/docs"
          className="rounded-lg bg-fd-primary px-4 py-2 text-sm font-medium text-fd-primary-foreground"
        >
          阅读文档
        </Link>
        <Link
          href="/docs/backend/creation-platform-sdk"
          className="rounded-lg border px-4 py-2 text-sm font-medium"
        >
          创作平台 SDK
        </Link>
      </div>
    </main>
  );
}
