# devproxy documentation

This directory is a standalone [Fumadocs](https://fumadocs.dev/docs) app for devproxy. It uses the default `/docs` route and can be deployed separately from any product or marketing app.

Node.js 22 or newer is required to develop the site.

```sh
npm install
npm run dev
```

Open <http://localhost:3000/docs>. Before publishing changes, run `npm run build`.

Edit pages in `content/docs` and sidebar order in `content/docs/meta.json`. Do not edit `.source`; Fumadocs generates it.

## Publish on GitHub Pages

The [Publish docs](../.github/workflows/docs-pages.yml) workflow builds a static site and deploys `docs/out` on pushes to `main` that change the docs or workflow. It can also be run manually. The build sets `NEXT_PUBLIC_SITE_URL` to `https://devproxy.vietz.dev` for metadata links.

To finish the initial setup in the `vietz-dev/devproxy` repository:

1. In **Settings → Pages**, select **GitHub Actions** as the build and deployment source. Set the custom domain to `devproxy.vietz.dev` and save it.
2. At the DNS provider for `vietz.dev`, replace the current `devproxy` record with a `CNAME` record pointing to `vietz-dev.github.io`. Do not include the repository name in the target.
3. Run **Publish docs** from the Actions tab or push a docs change to `main`. After the certificate is issued, enable **Enforce HTTPS** in Pages settings.

The domain is configured in Pages settings, not through a `CNAME` file in this workflow's artifact. DNS changes and certificate issuance can take time to propagate.
