import { PHASE_DEVELOPMENT_SERVER } from "next/constants.js";

/** @type {import('next').NextConfig} */
const sharedConfig = {
  trailingSlash: true,
  generateBuildId: async () => "windforce-lite",
};

const exportConfig = {
  ...sharedConfig,
  assetPrefix: "/ui",
  basePath: "/ui",
  output: "export",
};

export default function nextConfig(phase) {
  if (phase !== PHASE_DEVELOPMENT_SERVER) return exportConfig;
  const apiTarget = process.env.WINDFORCE_LITE_API_PROXY_TARGET || "http://127.0.0.1:18091";
  return {
    ...sharedConfig,
    async rewrites() {
      return [
        {
          source: "/ui",
          destination: "/",
        },
        {
          source: "/ui/:path*",
          destination: "/:path*",
        },
        {
          source: "/api/:path*",
          destination: `${apiTarget}/api/:path*`,
        },
        {
          source: "/healthz",
          destination: `${apiTarget}/healthz`,
        },
        {
          source: "/readyz",
          destination: `${apiTarget}/readyz`,
        },
      ];
    },
  };
}
