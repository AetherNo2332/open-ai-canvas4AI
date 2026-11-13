import { createMDX } from 'fumadocs-mdx/next';

const withMDX = createMDX();

/** @type {import('next').NextConfig} */
const config = {
  reactStrictMode: true,
  basePath: '/docs',
  output: 'standalone',
  env: {
    DOCS_BUILD_ID: process.env.DOCS_BUILD_ID || 'development',
    DOCS_BUILD_COMMIT: process.env.DOCS_BUILD_COMMIT || 'unknown',
    DOCS_BUILD_TIME: process.env.DOCS_BUILD_TIME || 'unknown',
  },
};

export default withMDX(config);
