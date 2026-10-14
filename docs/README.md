# devproxy documentation

This directory is a standalone [Fumadocs](https://fumadocs.dev/docs) app for devproxy. It uses the default `/docs` route and can be deployed separately from any product or marketing app.

Node.js 22 or newer is required to develop the site.

```sh
npm install
npm run dev
```

Open <http://localhost:3000/docs>. Before publishing changes, run `npm run build`.

Edit pages in `content/docs` and sidebar order in `content/docs/meta.json`. Do not edit `.source`; Fumadocs generates it.

Configure your hosting provider with this directory as the project root and `npm run build` as the build command. Set `NEXT_PUBLIC_SITE_URL` to the deployment origin (for example, `https://docs.example.com`) so generated Open Graph links use that domain. Vercel deployments can also use their built-in `VERCEL_URL`. To use a subdomain, point it at the docs deployment; to serve docs under a main site, forward `/docs/*` to that deployment.
