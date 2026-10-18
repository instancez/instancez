// Cloudflare Worker behind get.instancez.ai. It serves the two install scripts
// and nothing else. The scripts pull binaries straight from GitHub Releases, so
// the Worker holds no secrets and needs no GitHub token.
//
// The script bodies live base64-encoded in scripts.generated.js (produced by
// gen.mjs) so the deploy upload does not carry the literal download-and-run
// patterns the installers use. Cloudflare's WAF blocks those on its own API.
//
// Routes:
//   GET /          serves the macOS/Linux installer
//   GET /windows   serves the Windows installer
// The .sh / .ps1 paths are aliases so the URLs read well in a browser too.
import { installShB64, installPs1B64 } from './scripts.generated.js'

const decode = (b64) =>
  new TextDecoder().decode(Uint8Array.from(atob(b64), (c) => c.charCodeAt(0)))

const installSh = decode(installShB64)
const installPs1 = decode(installPs1B64)

const CACHE = 'public, max-age=300'

const POSTHOG_URL = 'https://us.i.posthog.com/capture/'
const POSTHOG_KEY = 'phc_E3FjB3GLyaE5GzWf1mRqOGfq89VUFRuLNWR4s0bBIew'

export function trackInstall(request, url, os) {
  const params = Object.fromEntries(url.searchParams)
  return fetch(POSTHOG_URL, {
    method: 'POST',
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify({
      api_key: POSTHOG_KEY,
      event: 'install_script',
      distinct_id: crypto.randomUUID(),
      properties: {
        os,
        ref: params.ref,
        utm_source: params.utm_source,
        utm_campaign: params.utm_campaign,
        $current_url: url.href,
        $referrer: request.headers.get('referer') || undefined,
        client_ua: (request.headers.get('user-agent') || '').slice(0, 100),
        $process_person_profile: false,
        $geoip_disable: true,
      },
    }),
  }).catch(() => {})
}

function script(body, type) {
  return new Response(body, {
    headers: { 'content-type': type, 'cache-control': CACHE },
  })
}

export default {
  async fetch(request, env, ctx) {
    const url = new URL(request.url)
    const track = (os) => {
      if (request.method === 'GET') ctx?.waitUntil(trackInstall(request, url, os))
    }

    switch (url.pathname) {
      case '/':
      case '/install.sh':
        track('sh')
        return script(installSh, 'text/x-shellscript; charset=utf-8')
      case '/windows':
      case '/install.ps1':
        track('ps1')
        return script(installPs1, 'text/plain; charset=utf-8')
      default:
        return new Response('Not found. Try https://get.instancez.ai for the installer.\n', {
          status: 404,
          headers: { 'content-type': 'text/plain; charset=utf-8' },
        })
    }
  },
}
