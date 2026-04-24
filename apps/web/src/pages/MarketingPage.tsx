import type { ReactElement } from 'react';
import { Link } from 'react-router';
import { MarketingMockup } from '../components/MarketingMockup';
import logo from '../assets/sliils-logo.png';
import '../styles/marketing.css';

// Distinctive editorial/operator aesthetic: Bricolage Grotesque pushed
// to its display optical size for headlines, JetBrains Mono for the
// technical marginalia, warm cream paper palette with sharp ink.

const statusSignals = [
  'v0.12 · late 2026',
  'self-hosted by default',
  'licensed AGPL-3.0',
  'postgres row-level security on',
  'no telemetry, no call-home',
  'built with Go + React',
  'meant for 2 – 50 people',
  'designed for operators',
];

const features = [
  {
    title: 'Channels, threads, search, files.',
    copy: 'The daily surface area of team chat, treated as the primary product — not a loss-leader for upsells you never asked for.',
  },
  {
    title: 'Run it where your data already lives.',
    copy: 'Go binary, Postgres, object storage of your choosing. Your infra, your boundary, your backups. No call home.',
  },
  {
    title: 'Sized for teams that still know each other.',
    copy: 'Two to fifty people. Fast to onboard, light on chrome, free from the sprawl that enterprise suites inherit by default.',
  },
  {
    title: 'Multi-tenant when you need it.',
    copy: 'Studios, agencies, operators: spin up multiple workspaces inside one install. Per-tenant email, per-tenant boundaries.',
  },
];

const principles = [
  'Magic-link and password flows, email verification, and password reset are native — no third-party auth shim to break on a Tuesday.',
  'Attachments are content-addressed and deduplicated per workspace, ready for pluggable storage drivers when you outgrow local disk.',
  'The UI stays focused on the conversation: navigation, threads, and search layered into the daily flow rather than hidden in menus.',
];

const rolloutSteps = [
  {
    label: 'deploy',
    title: 'Stand up the stack.',
    copy: 'One Go binary, a Postgres database, and a reverse proxy. Local in an afternoon; production on the same afternoon if you like.',
  },
  {
    label: 'invite',
    title: 'Bring the team in.',
    copy: 'First-run wizard creates the super-admin, the first workspace, and the email provider. Invite links do the rest.',
  },
  {
    label: 'own',
    title: 'Keep it in-house.',
    copy: 'Messages, files, settings, and every audit record live on hardware you chose. Nothing leaves unless you say so.',
  },
];

const faqs = [
  {
    question: 'Who is this built for?',
    answer:
      'Small companies, internal teams, consultancies, and community groups who want a Slack or Teams workflow without surrendering hosting control. If you already know why you want to self-host, this is for you.',
  },
  {
    question: 'What makes it different from a generic chat clone?',
    answer:
      'The product is shaped around durable team workflows: workspace setup, authentication, search, attachments, and threaded collaboration behave as a coherent whole. Self-hosting is the default path, not a later-gated feature.',
  },
  {
    question: 'Can I try the actual application?',
    answer:
      'Yes. Sign up from this page and the existing product flows take it from there — no separate demo stack, no placeholder screenshots standing in for software that does not exist.',
  },
  {
    question: 'Does it try to be every workplace app at once?',
    answer:
      'No. Messaging, threads, files, and the operator surface are the point. Pages and calendar are there when teams need them; they are not the trojan horse for a suite.',
  },
];

export function MarketingPage(): ReactElement {
  return (
    <main className="marketing-page">
      <div className="marketing-status" role="status" aria-label="Install status">
        <span className="marketing-status__dot" aria-hidden="true" />
        <div className="marketing-status__track">
          <div className="marketing-status__rail" aria-hidden="true">
            {[...statusSignals, ...statusSignals].map((s, i) => (
              <span key={`${s}-${i}`}>{s}</span>
            ))}
          </div>
        </div>
      </div>

      <header className="marketing-nav">
        <Link to="/" className="marketing-brand" aria-label="SliilS">
          <img src="/favicon.png" alt="" className="marketing-brand__icon" />
          <div className="marketing-brand__copy">
            <img src={logo} alt="SliilS" className="marketing-brand__wordmark" />
            <span>SELF-HOSTED · TEAM CHAT</span>
          </div>
        </Link>

        <nav className="marketing-nav__links" aria-label="Marketing sections">
          <a href="#what">What it is</a>
          <a href="#how">How to run it</a>
          <a href="#faq">Questions</a>
        </nav>

        <div className="marketing-nav__actions">
          <Link to="/login" className="marketing-button marketing-button--ghost">
            Open app
          </Link>
          <Link to="/signup" className="marketing-button marketing-button--primary">
            Start a workspace
            <span className="marketing-button__arrow" aria-hidden="true">→</span>
          </Link>
        </div>
      </header>

      <section className="marketing-hero">
        <span className="marketing-hero__label">Release 0.12 — bringing chat in-house</span>
        <h1>
          Team chat you can <em>actually</em> keep on your own hardware.
        </h1>
        <p className="marketing-hero__lead">
          SliilS is a self-hosted alternative to Slack and Teams for small teams that want
          channels, threads, search, files, and clean workspace boundaries — without a
          per-seat tax, without telemetry, without waiting for someone else&rsquo;s
          shard to come back.
        </p>
        <div className="marketing-hero__meta">
          <div className="marketing-hero__cta">
            <Link to="/signup" className="marketing-button marketing-button--primary">
              Create your workspace
              <span className="marketing-button__arrow" aria-hidden="true">→</span>
            </Link>
            <Link to="/login" className="marketing-button marketing-button--secondary">
              Sign in
            </Link>
          </div>
          <aside className="marketing-hero__aside" aria-label="At-a-glance facts">
            <span>
              <strong>2 – 50</strong>
              Team size range
            </span>
            <span>
              <strong>Go · Postgres</strong>
              Stack you already know
            </span>
            <span>
              <strong>AGPL-3.0</strong>
              Source-available licence
            </span>
          </aside>
        </div>
      </section>

      <div className="marketing-mockup-wrap">
        <div className="marketing-mockup-caption">
          <div>
            <strong>fig. 01 — a working channel</strong>
            Rendered from the live product. No staged stock imagery.
          </div>
          <span>launch-week · 09:24</span>
        </div>
        <MarketingMockup />
      </div>

      <section id="what" className="marketing-section">
        <span className="marketing-section__tag">§ 01</span>
        <div className="marketing-section__heading">
          <span className="marketing-section__numeral">01</span>
          <div>
            <h2>Trim the suite. Keep the collaboration surface your team actually uses.</h2>
            <p>
              SliilS is shaped around the day-to-day — the conversation, the handoff, the
              file you need to find again next Thursday — instead of bundling every
              possible office workflow into one heavyweight subscription.
            </p>
          </div>
        </div>

        <div className="marketing-feature-grid">
          {features.map((feature) => (
            <article key={feature.title} className="marketing-feature-card">
              <h3>{feature.title}</h3>
              <p>{feature.copy}</p>
            </article>
          ))}
        </div>
      </section>

      <aside className="marketing-quote" aria-label="Pull quote">
        <div className="marketing-quote__inner">
          <span className="marketing-quote__mark" aria-hidden="true">&ldquo;</span>
          <div>
            <blockquote>
              No per-seat tax. No telemetry shipped to someone else&rsquo;s warehouse.
              No <em>pager</em> when their shard goes down.
            </blockquote>
            <cite>— the operating premise, in one line</cite>
          </div>
        </div>
      </aside>

      <section className="marketing-section marketing-section--split">
        <span className="marketing-section__tag">§ 02</span>
        <div className="marketing-panel--highlight">
          <span className="marketing-kicker">Practical foundations</span>
          <h2>Made for operators who care where data lives and how the system behaves.</h2>
          <p>
            A Go backend, React web client, shared schemas, search integration, and an
            attachment pipeline built for predictable storage behaviour. Everything is
            accountable, everything is readable, nothing is a black box.
          </p>
        </div>

        <div className="marketing-panel--list">
          {principles.map((item) => (
            <div key={item} className="marketing-check">
              <span className="marketing-check__bullet" aria-hidden="true" />
              <p>{item}</p>
            </div>
          ))}
        </div>
      </section>

      <section id="how" className="marketing-section">
        <span className="marketing-section__tag">§ 03</span>
        <div className="marketing-section__heading">
          <span className="marketing-section__numeral">02</span>
          <div>
            <h2>Three steps, no sales call.</h2>
            <p>
              Small enough for one person to stand up on a Saturday; structured enough to
              trust with a team&rsquo;s daily communication by Monday morning.
            </p>
          </div>
        </div>

        <div className="marketing-steps">
          {rolloutSteps.map((step) => (
            <article key={step.title} className="marketing-step">
              <span className="marketing-step__index">{step.label}</span>
              <h3>{step.title}</h3>
              <p>{step.copy}</p>
            </article>
          ))}
        </div>
      </section>

      <section id="faq" className="marketing-section">
        <span className="marketing-section__tag">§ 04</span>
        <div className="marketing-section__heading">
          <span className="marketing-section__numeral">03</span>
          <div>
            <h2>Questions teams usually ask before they bring chat back in-house.</h2>
          </div>
        </div>

        <div className="marketing-faqs">
          {faqs.map((item) => (
            <article key={item.question} className="marketing-faq">
              <h3>{item.question}</h3>
              <p>{item.answer}</p>
            </article>
          ))}
        </div>
      </section>

      <section className="marketing-section" style={{ borderTop: 'none', paddingTop: 0 }}>
        <div className="marketing-cta-panel">
          <div>
            <span className="marketing-kicker">Ready when you are</span>
            <h2>
              Bring the conversation <em>home</em>.
            </h2>
          </div>
          <div className="marketing-cta">
            <Link to="/signup" className="marketing-button marketing-button--primary">
              Start a workspace
              <span className="marketing-button__arrow" aria-hidden="true">→</span>
            </Link>
            <Link to="/login" className="marketing-button marketing-button--secondary">
              Sign in
            </Link>
          </div>
        </div>
      </section>

      <footer className="marketing-footer">
        <div>
          <div className="marketing-footer__mark">SliilS</div>
          <div style={{ marginTop: '0.4rem' }}>Self-hosted team collaboration · keep your own house</div>
        </div>
        <div className="marketing-footer__meta">
          <span>v0.12-alpha</span>
          <span>AGPL-3.0</span>
          <span>no telemetry</span>
          <span>made for small teams</span>
        </div>
      </footer>
    </main>
  );
}
