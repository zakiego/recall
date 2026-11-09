import { HomeLayout } from "fumadocs-ui/layouts/home"
import Link from "next/link"
import { baseOptions } from "@/lib/layout.shared"

const FOOTER_LINKS = [
  { href: "/docs", text: "Docs" },
  { href: "/privacy", text: "Privacy" },
  { href: "/terms", text: "Terms" },
]

export default function Layout({ children }: LayoutProps<"/">) {
  return (
    <>
      <HomeLayout {...baseOptions()}>{children}</HomeLayout>
      <footer className="border-t">
        <div className="mx-auto flex w-full max-w-6xl flex-wrap items-center justify-between gap-x-4 gap-y-3 px-4 py-6 text-sm text-muted-foreground md:px-6">
          <p>
            <span className="font-mono">recall.zakiego.com</span>
            <span aria-hidden="true"> · </span>
            built by{" "}
            <a href="https://zakiego.com" className="font-medium text-foreground underline underline-offset-4">
              Zaki
            </a>
          </p>
          <nav aria-label="Footer" className="flex items-center gap-4">
            {FOOTER_LINKS.map((l) => (
              <Link key={l.href} href={l.href} className="font-medium text-foreground underline underline-offset-4">
                {l.text}
              </Link>
            ))}
          </nav>
        </div>
      </footer>
    </>
  )
}
