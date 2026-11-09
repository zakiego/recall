import { HomeLayout } from "fumadocs-ui/layouts/home"
import Link from "next/link"
import { baseOptions } from "@/lib/layout.shared"

export default function Layout({ children }: LayoutProps<"/">) {
  return (
    <>
      <HomeLayout {...baseOptions()}>{children}</HomeLayout>
      <footer className="border-t">
        <div className="mx-auto flex w-full max-w-6xl items-center justify-between gap-4 px-4 py-6 text-sm text-muted-foreground md:px-6">
          <p>
            <span className="font-mono">recall.zakiego.com</span>
            <span aria-hidden="true"> · </span>
            built by{" "}
            <a href="https://zakiego.com" className="font-medium text-foreground underline underline-offset-4">
              Zaki
            </a>
          </p>
          <Link href="/docs" className="font-medium text-foreground underline underline-offset-4">
            Docs
          </Link>
        </div>
      </footer>
    </>
  )
}
