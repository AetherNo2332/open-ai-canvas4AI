export function GET() {
  return Response.json({
    status: 'ok',
    buildId: process.env.DOCS_BUILD_ID,
    commit: process.env.DOCS_BUILD_COMMIT,
    builtAt: process.env.DOCS_BUILD_TIME,
    deploymentContract: 1,
  }, { headers: { 'Cache-Control': 'no-store' } });
}
