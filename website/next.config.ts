import type { NextConfig } from "next";
import createMDX from "@next/mdx";

const isDev = process.env.NODE_ENV !== "production";

const cspHeader = `
  default-src 'self';
  script-src 'self' 'unsafe-inline' ${isDev ? "'unsafe-eval' 'wasm-unsafe-eval'" : "'wasm-unsafe-eval'"} https://vercel.live https://va.vercel-scripts.com;
  style-src 'self' 'unsafe-inline' https://vercel.live;
  img-src 'self' data: blob: https://vercel.live https://vercel.com https://*.vercel.com;
  font-src 'self' https://vercel.live https://assets.vercel.com;
  connect-src 'self' https://vercel.live wss://*.pusher.com https://va.vercel-scripts.com;
  frame-src 'self' https://vercel.live;
  object-src 'none';
  base-uri 'self';
  form-action 'self';
`
  .replace(/\n/g, " ")
  .trim();

const withMDX = createMDX({});

const nextConfig: NextConfig = {
  pageExtensions: ["js", "jsx", "md", "mdx", "ts", "tsx"],
  headers: async () => [
    {
      source: "/(.*)",
      headers: [{ key: "Content-Security-Policy", value: cspHeader }],
    },
  ],
};

export default withMDX(nextConfig);
