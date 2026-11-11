import type { NextConfig } from "next";

// The browser talks only to this Next.js origin; /api/* is proxied to the Go backend.
const backend = process.env.BACKEND_URL ?? "http://localhost:8080";

const nextConfig: NextConfig = {
  output: "standalone",
  // hide the Next.js "N" development badge in the corner
  devIndicators: false,
  cacheComponents: true,
  partialPrefetching: true,
  experimental: {
    // Uploads pass through the /api rewrite proxy, which buffers bodies (default 10MB, larger
    // bodies are silently truncated). Keep this a bit above the backend's MAX_UPLOAD_MB so the
    // backend, not the proxy, rejects oversized files with a clear 413 message.
    proxyClientMaxBodySize: "55mb",
  },
  turbopack: {
    rules: {
      "*.css": {
        loaders: ["@tailwindcss/turbopack"],
        as: "*.css",
      },
    },
  },
  async rewrites() {
    return [{ source: "/api/:path*", destination: `${backend}/api/:path*` }];
  },
};

export default nextConfig;
