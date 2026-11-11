import { describe, expect, it, vi } from "vitest"
import { fetchUrl, isPrivateAddress, MAX_REDIRECTS, type Resolve } from "./content"

// Fake DNS: public names get a documentation-free public address, the rest
// whatever the table says. Nothing here touches the network.
const DNS: Record<string, string[]> = {
  "example.com": ["93.184.215.14"],
  "cdn.example.com": ["2606:4700::6810:85e5"],
  "localhost": ["127.0.0.1", "::1"],
  "intranet.example.com": ["10.0.0.5"],
  "mixed.example.com": ["93.184.215.14", "192.168.1.10"],
  "metadata.example.com": ["169.254.169.254"],
  "ula.example.com": ["fd12:3456::1"],
}
const resolve: Resolve = async (host) => {
  const found = DNS[host]
  if (!found) throw new Error(`getaddrinfo ENOTFOUND ${host}`)
  return found
}

function page(body: string, init: ResponseInit = {}) {
  return new Response(body, { status: 200, headers: { "content-type": "text/plain" }, ...init })
}
const redirect = (location: string, status = 302) => new Response(null, { status, headers: { location } })

function fakeFetch(handler: (url: URL) => Response) {
  return vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    expect(init?.redirect).toBe("manual")
    return handler(new URL(input instanceof Request ? input.url : input))
  })
}

describe("isPrivateAddress", () => {
  it.each([
    "127.0.0.1",
    "127.8.8.8",
    "0.0.0.0",
    "10.1.2.3",
    "172.16.0.1",
    "172.31.255.255",
    "192.168.1.1",
    "169.254.169.254",
    "100.64.0.1",
    "100.127.255.255",
    "224.0.0.1",
    "255.255.255.255",
    "::",
    "::1",
    "::ffff:127.0.0.1",
    "::ffff:7f00:1",
    "::ffff:10.0.0.1",
    "::ffff:8.8.8.8",
    "fc00::1",
    "fd12:3456::1",
    "fe80::1",
    "ff02::1",
    "not-an-ip",
  ])("blocks %s", (a) => expect(isPrivateAddress(a)).toBe(true))

  it.each(["8.8.8.8", "93.184.215.14", "172.32.0.1", "100.128.0.1", "2606:4700::6810:85e5", "2001:4860:4860::8888"])("allows %s", (a) =>
    expect(isPrivateAddress(a)).toBe(false),
  )
})

describe("fetchUrl", () => {
  it("fetches a public page", async () => {
    const f = fakeFetch(() => page("hello from the web"))
    const r = await fetchUrl("https://example.com/notes.txt", f, resolve)
    expect(r.text).toBe("hello from the web")
    expect(f).toHaveBeenCalledOnce()
  })

  it.each([
    "http://127.0.0.1:8793/v1/chat",
    "http://localhost:4800/v1/documents",
    "http://[::1]:8793/",
    "http://[::ffff:127.0.0.1]:8793/",
    "http://2130706433:8793/",
    "http://0x7f.1/",
    "http://intranet.example.com/",
    "http://mixed.example.com/",
    "http://metadata.example.com/latest/meta-data/",
    "http://169.254.169.254/latest/meta-data/",
    "http://ula.example.com/",
  ])("refuses %s without fetching it", async (url) => {
    const f = fakeFetch(() => page("secret"))
    await expect(fetchUrl(url, f, resolve)).rejects.toThrow(/private or local address/)
    expect(f).not.toHaveBeenCalled()
  })

  it("refuses a redirect into a private address", async () => {
    const f = fakeFetch(() => redirect("http://127.0.0.1:8793/v1/models"))
    await expect(fetchUrl("https://example.com/go", f, resolve)).rejects.toThrow(/private or local address/)
    expect(f).toHaveBeenCalledOnce()
  })

  it("follows public redirects, relative ones too", async () => {
    const f = fakeFetch((u) => {
      if (u.href === "https://example.com/a") return redirect("https://cdn.example.com/b", 301)
      if (u.href === "https://cdn.example.com/b") return redirect("/c", 307)
      return page(`landed on ${u.href}`)
    })
    const r = await fetchUrl("https://example.com/a", f, resolve)
    expect(r.text).toBe("landed on https://cdn.example.com/c")
    expect(f).toHaveBeenCalledTimes(3)
  })

  it("gives up after too many redirects", async () => {
    let n = 0
    const f = fakeFetch(() => redirect(`https://example.com/${++n}`))
    await expect(fetchUrl("https://example.com/0", f, resolve)).rejects.toThrow(`more than ${MAX_REDIRECTS} times`)
    expect(f).toHaveBeenCalledTimes(MAX_REDIRECTS + 1)
  })

  it("refuses a redirect to another protocol", async () => {
    const f = fakeFetch(() => redirect("file:///etc/passwd"))
    await expect(fetchUrl("https://example.com/", f, resolve)).rejects.toThrow(/only http and https/)
  })

  it("refuses a non-http URL up front", async () => {
    const f = fakeFetch(() => page("x"))
    await expect(fetchUrl("ftp://example.com/x", f, resolve)).rejects.toThrow(/only http and https/)
    expect(f).not.toHaveBeenCalled()
  })
})
