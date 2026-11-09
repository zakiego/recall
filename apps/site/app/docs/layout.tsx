import { DocsLayout } from "fumadocs-ui/layouts/docs"
import { baseOptions, dashboardLink } from "@/lib/layout.shared"
import { source } from "@/lib/source"

export default function Layout({ children }: LayoutProps<"/docs">) {
  // The sidebar already is the docs, so it only keeps the dashboard link.
  return (
    <DocsLayout tree={source.getPageTree()} {...baseOptions()} links={[dashboardLink]}>
      {children}
    </DocsLayout>
  )
}
