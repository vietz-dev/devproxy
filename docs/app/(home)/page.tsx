import type { Metadata } from 'next';
import Link from 'next/link';
import { ArrowRight, ArrowUpRight, Check, GitBranch, Globe2, LockKeyhole, Terminal } from 'lucide-react';
import './landing.css';

export const metadata: Metadata = {
  title: { absolute: 'devproxy — local HTTPS for every worktree' },
  description: 'Give every agentic development worktree its own process, port, and predictable HTTPS URL. devproxy connects them through a shared local Caddy proxy.',
};

const runtimes = [
  { branch: 'main', url: 'fernbook-main.localhost', port: '4321', tone: 'blue' },
  { branch: 'auth-flow', url: 'fernbook-auth-flow.localhost', port: '5173', tone: 'violet' },
  { branch: 'checkout', url: 'fernbook-checkout.localhost', port: '3888', tone: 'orange' },
] as const;

export default function HomePage() {
  return (
    <main className="landing">
      <section className="landing-hero" aria-labelledby="hero-title">
        <div className="landing-container landing-hero-grid">
          <div className="landing-hero-copy">
            <div className="landing-eyebrow"><span className="landing-eyebrow-dot" /> Local development, in parallel</div>
            <h1 id="hero-title">Give every agent<br />its own <span>runtime.</span></h1>
            <p className="landing-hero-lede">Run multiple Git worktrees side by side. devproxy gives each one a separate process, port, and predictable HTTPS URL named for its project and worktree, so agents and humans always know which app they are opening.</p>
            <div className="landing-actions">
              <Link href="/docs/getting-started" className="landing-button landing-button-primary">Get started <ArrowRight size={17} aria-hidden="true" /></Link>
              <Link href="/docs" className="landing-button landing-button-secondary">Read the docs <ArrowUpRight size={17} aria-hidden="true" /></Link>
            </div>
            <div className="landing-hero-proof">
              <span><Check size={15} aria-hidden="true" /> One URL per worktree</span>
              <span><Check size={15} aria-hidden="true" /> Local HTTPS</span>
              <span><Check size={15} aria-hidden="true" /> No port guessing</span>
            </div>
          </div>

          <div className="landing-visual" role="img" aria-label="Three worktrees, each with its own local HTTPS URL, connected through one Caddy proxy">
            <div className="landing-visual-topline"><span className="landing-visual-label">YOUR PROJECT / FERNBOOK</span><span className="landing-visual-live"><span /> 3 runtimes live</span></div>
            <div className="landing-runtime-list">
              {runtimes.map((runtime) => (
                <div className={`landing-runtime landing-runtime-${runtime.tone}`} key={runtime.branch}>
                  <div className="landing-runtime-icon"><GitBranch size={18} aria-hidden="true" /></div>
                  <div className="landing-runtime-identity"><span className="landing-runtime-branch">{runtime.branch}</span><span className="landing-runtime-url"><LockKeyhole size={12} aria-hidden="true" /> {runtime.url}</span></div>
                  <span className="landing-runtime-port">:{runtime.port}</span>
                </div>
              ))}
            </div>
            <div className="landing-routing" aria-hidden="true"><div /><span /><div /></div>
            <div className="landing-caddy"><div className="landing-caddy-icon"><Globe2 size={20} aria-hidden="true" /></div><div><strong>One local Caddy proxy</strong><span>TLS certificates + traffic routing</span></div><span className="landing-caddy-status">SHARED</span></div>
            <div className="landing-visual-caption">Predictable origins for every checkout.</div>
          </div>
        </div>
      </section>

      <section className="landing-section landing-story" aria-labelledby="story-title">
        <div className="landing-container">
          <div className="landing-section-heading"><p className="landing-kicker">BUILT FOR PARALLEL WORK</p><h2 id="story-title">Independent worktrees deserve independent runtimes.</h2><p>An agent can work on authentication while another works on checkout. Give each checkout its own running app and a URL that says exactly where it leads.</p></div>
          <div className="landing-feature-grid">
            <article className="landing-feature"><div className="landing-feature-number">01 / PROCESS</div><div className="landing-feature-icon"><Terminal size={24} aria-hidden="true" /></div><h3>Run side by side</h3><p>Start the dev command in each worktree. devproxy assigns an available port and keeps its route registered while the process runs.</p></article>
            <article className="landing-feature"><div className="landing-feature-number">02 / IDENTITY</div><div className="landing-feature-icon"><GitBranch size={24} aria-hidden="true" /></div><h3>Know which project is open</h3><p>Each URL combines the project and worktree names: <code>https://fernbook-checkout.localhost</code>. Share it with an agent or open it yourself.</p></article>
            <article className="landing-feature"><div className="landing-feature-number">03 / HTTPS</div><div className="landing-feature-icon"><LockKeyhole size={24} aria-hidden="true" /></div><h3>Develop with a real origin</h3><p>A dedicated local Caddy instance handles HTTPS and forwards traffic to the right process, including WebSocket connections for hot reload.</p></article>
          </div>
        </div>
      </section>

      <section className="landing-section landing-scenario" aria-labelledby="scenario-title">
        <div className="landing-container">
          <div className="landing-section-heading"><p className="landing-kicker">A REALISTIC WORKDAY</p><h2 id="scenario-title">Four agents can move across two projects at once.</h2><p>Each agent owns a feature worktree and a running app. The URL names both the project and that worktree, while devproxy keeps the shared proxy up to date.</p></div>
          <p className="landing-scenario-hint">Scroll the diagram sideways to see both projects →</p>
          <iframe className="landing-scenario-frame" src="/parallel-work.html" title="Example of four agents working in two projects through devproxy" loading="lazy" />
          <Link href="/parallel-work.html" target="_blank" rel="noopener noreferrer" className="landing-scenario-link">Open diagram in a new tab <ArrowUpRight size={15} aria-hidden="true" /></Link>
        </div>
      </section>

      <section className="landing-section landing-example" aria-labelledby="example-title">
        <div className="landing-container landing-example-grid">
          <div className="landing-example-copy"><p className="landing-kicker">A SHORTER PATH TO A LIVE APP</p><h2 id="example-title">Start a command. Get a URL.</h2><p>Run devproxy from a worktree. It finds a free port, starts your app, and registers the route with Caddy. The URL follows the worktree name, even when its assigned port changes.</p><Link href="/docs/getting-started" className="landing-text-link">Follow the setup guide <ArrowRight size={17} aria-hidden="true" /></Link></div>
          <div className="landing-terminal"><div className="landing-terminal-bar"><div className="landing-terminal-dots" aria-hidden="true"><i /><i /><i /></div><span>~/fernbook-checkout</span><span>zsh</span></div><div className="landing-terminal-body"><p><span className="landing-terminal-prompt">$</span> devproxy run --project fernbook web -- next dev -p &apos;{'{port}'}&apos;</p><p className="landing-terminal-success">devproxy: web → <span>https://fernbook-checkout.localhost</span> on port 3888</p></div></div>
        </div>
      </section>

      <section className="landing-bottom" aria-labelledby="bottom-title"><div className="landing-container landing-bottom-inner"><div><p className="landing-kicker">READY FOR THE NEXT WORKTREE?</p><h2 id="bottom-title">Make every local app easy to find.</h2></div><Link href="/docs/getting-started" className="landing-button landing-button-primary">Set up devproxy <ArrowRight size={17} aria-hidden="true" /></Link></div></section>
    </main>
  );
}
