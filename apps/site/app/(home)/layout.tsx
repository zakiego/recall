import { HomeLayout } from "fumadocs-ui/layouts/home"
import Link from "next/link"
import { baseOptions } from "@/lib/layout.shared"

export default function Layout({ children }: LayoutProps<"/">) {
  return (
    <>
      <HomeLayout {...baseOptions()}>{children}</HomeLayout>
      <footer className="border-t">
        <div className="mx-auto flex w-full max-w-6xl flex-col gap-2 px-4 py-8 text-sm text-muted-foreground sm:flex-row sm:items-center sm:justify-between md:px-6">
          <p>
            Built by{" "}
            <a href="https://zakiego.com" className="font-medium text-foreground underline underline-offset-4">
              Zaki
            </a>
            . Runs on a Mac mini, with this site on Cloudflare Pages.
          </p>
          <Link href="/docs" className="font-medium text-foreground underline underline-offset-4">
            Docs
          </Link>
        </div>
      </footer>
    </>
  )
}
