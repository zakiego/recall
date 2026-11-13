import { Inter } from "next/font/google"

// The landing's type, after Linear: Inter with its optical-size axis. Geist
// Mono, which the root layout already loads, stays the mono. The docs keep
// Geist.
export const inter = Inter({
  subsets: ["latin"],
  axes: ["opsz"],
  variable: "--font-inter",
  display: "swap",
})
