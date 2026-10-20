import type { Metadata } from 'next';
import Link from 'next/link';
import { ArrowRight, ArrowUpRight, GitBranch, LockKeyhole, Terminal } from 'lucide-react';
import './landing.css';

export const metadata: Metadata = {
  title: { absolute: 'devproxy — one local HTTPS address per worktree' },
  description: 'Run apps in parallel Git worktrees with predictable local HTTPS URLs. devproxy assigns ports, registers routes, and keeps each checkout easy to find.',
};

const worktrees = [
  { name: 'main', address: 'fernbook-main.localhost', port: '4321' },
  { name: 'auth-flow', address: 'fernbook-auth-flow.localhost', port: '5173' },
  { name: 'checkout', address: 'fernbook-checkout.localhost', port: '3888' },
] as const;

export default function HomePage() {
  return (
    <main className="landing" id="main">
      <section className="landing-hero" aria-labelledby="hero-title">
        <div className="landing-container">
          <div className="landing-hero-copy">
            <p className="landing-brand">devproxy<span aria-hidden="true">_</span></p>
            <h1 id="hero-title">A local address<br />for every <em>worktree.</em></h1>
            <p className="landing-hero-lede">Run agents in parallel without losing track of their apps. devproxy starts the app in each worktree on a free port and gives it a predictable HTTPS URL, so the right checkout is always one click away.</p>
            <div className="landing-actions">
              <Link href="/docs/getting-started" className="landing-button landing-button-primary">Get started <ArrowRight size={17} aria-hidden="true" /></Link>
              <Link href="/docs" className="landing-button landing-button-secondary">Read the docs <ArrowUpRight size={16} aria-hidden="true" /></Link>
            </div>
            <p className="landing-hero-note">One Go binary · local Caddy · macOS and Linux</p>
          </div>

          <div className="landing-demo" aria-label="Example devproxy command and the local URL it creates">
            <div className="landing-demo-head"><span>THE WORKTREE LOOP</span><span>YOUR MACHINE / NO HOSTED SERVICE</span></div>
            <div className="landing-demo-body">
              <p className="landing-demo-context">~/fernbook/auth-flow</p>
              <div className="landing-command"><span className="landing-prompt">$</span><code>devproxy run --project fernbook web -- next dev -p &apos;{'{port}'}&apos;</code></div>
              <div className="landing-demo-result"><span className="landing-result-line" aria-hidden="true" /><span className="landing-result-label">OPEN THIS WORKTREE AT</span><strong><LockKeyhole size={17} aria-hidden="true" /> fernbook-auth-flow.localhost</strong></div>
            </div>
            <div className="landing-demo-foot"><span>Port allocated automatically</span><span>Route lives while the command runs</span></div>
          </div>
        </div>
      </section>

      <section className="landing-section landing-problem" aria-labelledby="problem-title">
        <div className="landing-container landing-two-column">
          <h2 id="problem-title">More worktrees.<br />More moving parts.<br /><span>One way to find them.</span></h2>
          <div className="landing-problem-copy"><p>One agent works on authentication. Another builds checkout. Both need to run the app, and both need to know which version the browser is showing.</p><p>Ports are temporary. Worktree names mean something. devproxy makes the project and worktree name the address you use every time.</p></div>
        </div>
      </section>

      <section className="landing-section landing-routing-section" aria-labelledby="routing-title">
        <div className="landing-container">
          <div className="landing-section-intro"><div><h2 id="routing-title">The URL tells you what is running.</h2><p>Each checkout gets its own process and route through one local Caddy instance.</p></div><span className="landing-intro-aside">PROJECT / FERNBOOK</span></div>
          <div className="landing-route-table" role="table" aria-label="Example worktrees and local routes">
            <div className="landing-route-header" role="row"><span role="columnheader">WORKTREE</span><span role="columnheader">LOCAL HTTPS ADDRESS</span><span role="columnheader">PORT</span></div>
            {worktrees.map((worktree) => <div className="landing-route-row" role="row" key={worktree.name}><span className="landing-route-name" role="cell"><GitBranch size={16} aria-hidden="true" /> {worktree.name}</span><span className="landing-route-address" role="cell"><LockKeyhole size={15} aria-hidden="true" /> {worktree.address}</span><span className="landing-route-port" role="cell">:{worktree.port}</span></div>)}
          </div>
          <p className="landing-table-note">Illustrative ports. The address follows the project and worktree names even when a port changes.</p>
        </div>
      </section>

      <section className="landing-section landing-method" aria-labelledby="method-title">
        <div className="landing-container landing-method-grid">
          <div><h2 id="method-title">Built for the way agents actually work.</h2><p className="landing-method-lede">The CLI owns the workflow. The browser dashboard helps you inspect what is registered. Both talk to the same local daemon.</p><Link href="/docs/getting-started" className="landing-text-link">Follow the setup guide <ArrowRight size={17} aria-hidden="true" /></Link></div>
          <div className="landing-method-list">
            <article><span className="landing-method-icon"><Terminal size={19} aria-hidden="true" /></span><div><h3>Start a command, get a route.</h3><p><code>devproxy run</code> picks a free port, starts your app, registers its URL, and renews the route while the command runs.</p></div></article>
            <article><span className="landing-method-icon"><GitBranch size={19} aria-hidden="true" /></span><div><h3>Keep worktrees separate.</h3><p>Project and checkout names become a stable local origin. Open the right app without looking up a port or changing bookmarks.</p></div></article>
            <article><span className="landing-method-icon"><LockKeyhole size={19} aria-hidden="true" /></span><div><h3>Use HTTPS locally.</h3><p>A dedicated Caddy instance handles certificates and forwards traffic, including WebSockets for hot reload.</p></div></article>
          </div>
        </div>
      </section>

      <section className="landing-start" aria-labelledby="start-title"><div className="landing-container landing-start-inner"><div><h2 id="start-title">Give the next worktree an address.</h2><p>Start the local daemon once. Then run your app from any checkout.</p><div className="landing-start-command"><span>$</span><code>devproxy start</code></div></div><Link href="/docs/getting-started" className="landing-button landing-button-primary">Set up devproxy <ArrowRight size={17} aria-hidden="true" /></Link></div></section>
    </main>
  );
}
