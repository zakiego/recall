import { BookOpenIcon, FileQuestionIcon } from "lucide-react"
import type { Metadata } from "next"
import { HomeLayout } from "fumadocs-ui/layouts/home"
import Link from "next/link"
import { buttonVariants } from "@/components/ui/button"
import { Empty, EmptyContent, EmptyDescription, EmptyHeader, EmptyMedia, EmptyTitle } from "@/components/ui/empty"
import { baseOptions } from "@/lib/layout.shared"

// Next's default 404 had no way back and a second <title>. This one keeps the
// site's header and points at the two places worth going. Next adds the
// noindex itself.
export const metadata: Metadata = {
  title: "Page not found",
}

export default function NotFound() {
  return (
    <HomeLayout {...baseOptions()}>
      <div className="mx-auto flex w-full max-w-6xl flex-1 items-center px-4 py-20 md:px-6">
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <FileQuestionIcon />
            </EmptyMedia>
            <EmptyTitle>
              <h1>This page doesn&apos;t exist</h1>
            </EmptyTitle>
            <EmptyDescription>The link may be old, or a docs page may have moved. The docs list every page.</EmptyDescription>
          </EmptyHeader>
          <EmptyContent className="flex-row flex-wrap justify-center">
            {/* Real links styled as buttons, so they're announced and listed as links. */}
            <Link href="/docs" className={buttonVariants()}>
              <BookOpenIcon data-icon="inline-start" />
              Read the docs
            </Link>
            <Link href="/" className={buttonVariants({ variant: "outline" })}>
              Go to the home page
            </Link>
          </EmptyContent>
        </Empty>
      </div>
    </HomeLayout>
  )
}
