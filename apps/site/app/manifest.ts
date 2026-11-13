import type { MetadataRoute } from "next"
import { appName, siteDescription } from "@/lib/shared"

export const dynamic = "force-static"

export default function manifest(): MetadataRoute.Manifest {
  return {
    name: appName,
    short_name: appName,
    description: siteDescription,
    start_url: "/",
    display: "browser",
    background_color: "#08090a",
    theme_color: "#08090a",
    icons: [
      { src: "/icon.svg", type: "image/svg+xml", sizes: "any" },
      { src: "/apple-icon.png", type: "image/png", sizes: "180x180" },
    ],
  }
}
