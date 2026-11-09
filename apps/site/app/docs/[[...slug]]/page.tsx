import { source } from '@/lib/source';
import {
  DocsBody,
  DocsDescription,
  DocsPage,
  DocsTitle,
  MarkdownCopyButton,
  ViewOptionsPopover,
} from 'fumadocs-ui/layouts/docs/page';
import { notFound } from 'next/navigation';
import { getMDXComponents } from '@/components/mdx';
import type { Metadata } from 'next';
import { createRelativeLink } from 'fumadocs-ui/mdx';
import { getPageImageUrl, getPageMarkdownUrl } from '@/lib/shared';

export default async function Page(props: PageProps<'/docs/[[...slug]]'>) {
  const params = await props.params;
  const page = source.getPage(params.slug);
  if (!page) notFound();

  const MDX = page.data.body;
  const markdownUrl = getPageMarkdownUrl(page).url;

  return (
    <DocsPage toc={page.data.toc} full={page.data.full}>
      <DocsTitle>{page.data.title}</DocsTitle>
      <DocsDescription className="mb-0">{page.data.description}</DocsDescription>
      <div className="flex flex-row gap-2 items-center border-b pb-6">
        <MarkdownCopyButton markdownUrl={markdownUrl} />
        <ViewOptionsPopover markdownUrl={markdownUrl} />
      </div>
      <DocsBody>
        <MDX
          components={getMDXComponents({
            // this allows you to link to other pages with relative file paths
            a: createRelativeLink(source, page),
          })}
        />
      </DocsBody>
    </DocsPage>
  );
}

export async function generateStaticParams() {
  return source.generateParams();
}

export async function generateMetadata(props: PageProps<'/docs/[[...slug]]'>): Promise<Metadata> {
  const params = await props.params;
  const page = source.getPage(params.slug);
  if (!page) notFound();

  // Pages under /docs/api share names with the guides (Ask, Search), so their
  // titles say "API". The sidebar keeps the short name.
  const title = page.url.startsWith('/docs/api/') ? `${page.data.title} API` : page.data.title;
  // A shared link shows only og:title, so it carries the product name the
  // <title> template adds.
  const shareTitle = `${title} · Recall docs`;
  const image = { url: getPageImageUrl(page).url, width: 1200, height: 630, alt: `Recall docs: ${title}` };
  return {
    title,
    description: page.data.description,
    alternates: { canonical: page.url },
    openGraph: { type: 'article', siteName: 'Recall', locale: 'en_US', url: page.url, title: shareTitle, description: page.data.description, images: [image] },
    twitter: { card: 'summary_large_image', title: shareTitle, description: page.data.description, images: [image] },
  };
}
