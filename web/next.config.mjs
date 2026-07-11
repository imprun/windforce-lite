/** @type {import('next').NextConfig} */
const nextConfig = {
  output: "export",
  basePath: "/ui",
  assetPrefix: "/ui",
  trailingSlash: true,
  generateBuildId: async () => "windforce-lite",
};

export default nextConfig;
