import { createMDX } from "fumadocs-mdx/next"

const withMDX = createMDX()

/** @type {import('next').NextConfig} */
const config = {
  // A fully static site: `next build` writes it to out/, which Cloudflare
  // Pages serves as is. Nothing runs on a server.
  output: "export",
  images: { unoptimized: true },
  reactStrictMode: true,
}

export default withMDX(config)
