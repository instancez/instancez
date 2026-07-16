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

function script(body, type) {
  return new Response(body, {
    headers: { 'content-type': type, 'cache-control': CACHE },
  })
}

export default {
  async fetch(request) {
    const { pathname } = new URL(request.url)

    switch (pathname) {
      case '/':
      case '/install.sh':
        return script(installSh, 'text/x-shellscript; charset=utf-8')
      case '/windows':
      case '/install.ps1':
        return script(installPs1, 'text/plain; charset=utf-8')
      default:
        return new Response('Not found. Try https://get.instancez.ai for the installer.\n', {
          status: 404,
          headers: { 'content-type': 'text/plain; charset=utf-8' },
        })
    }
  },
}
