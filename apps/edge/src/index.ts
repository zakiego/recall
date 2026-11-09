import { isAppPath } from "./routing.ts"

interface Env {
  // The Pages project's own hostname, e.g. https://recall-site.pages.dev
  SITE_ORIGIN: string
  // Unset in production: app paths go to the zone's origin (the tunnel) with
  // fetch(request), untouched. Set only on the preview worker, which has no
  // route of its own and so has to name the origin it forwards to.
  APP_ORIGIN?: string
}

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const url = new URL(request.url)

    if (isAppPath(url.pathname)) {
      // On a route, a subrequest to the same hostname skips this worker and goes
      // straight to the origin. Method, headers, body, streaming responses,
      // Set-Cookie and redirects all pass through as they are.
      if (!env.APP_ORIGIN) return fetch(request)
      return fetch(new Request(rebase(url, env.APP_ORIGIN), request))
    }

    const res = await fetch(new Request(rebase(url, env.SITE_ORIGIN), request), { redirect: "manual" })
    return fixSiteRedirect(res, env.SITE_ORIGIN, url.origin)
  },
}

function rebase(url: URL, origin: string): string {
  const target = new URL(origin)
  target.pathname = url.pathname
  target.search = url.search
  return target.toString()
}

// Pages answers some paths with a redirect (/docs/ to /docs, say). Keep the
// visitor on the public hostname if it ever names its own.
function fixSiteRedirect(res: Response, siteOrigin: string, publicOrigin: string): Response {
  const location = res.headers.get("location")
  if (!location) return res
  const target = new URL(location, siteOrigin)
  if (target.origin !== new URL(siteOrigin).origin) return res
  const headers = new Headers(res.headers)
  headers.set("location", `${publicOrigin}${target.pathname}${target.search}${target.hash}`)
  return new Response(res.body, { status: res.status, statusText: res.statusText, headers })
}
