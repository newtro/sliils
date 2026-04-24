import type { ReactElement } from 'react';
import { Link } from 'react-router';
import { MarketingMockup } from '../components/MarketingMockup';
import logo from '../assets/sliils-logo.png';
import '../styles/marketing.css';

const featureCards = [
  {
    title: 'Built for real team communication',
    copy: 'Channels, threads, mentions, search, and file sharing are the core surface area, not an afterthought buried under enterprise bloat.',
  },
  {
    title: 'Self-hosted by default',
    copy: 'Run it on your infrastructure, keep your workspace data close, and choose storage backends that match how your team operates.',
  },
  {
    title: 'Designed for smaller teams',
    copy: 'SliilS is tuned for teams of roughly 2 to 50 people that want fast coordination without seat-based pricing pressure.',
  },
  {
    title: 'Multi-tenant friendly',
    copy: 'Useful for operators, studios, and agencies who need separate workspaces without spinning up a completely different stack each time.',
  },
];

const operatingPrinciples = [
  'Email + password, magic-link sign-in, password reset, and email verification flows are already part of the product foundation.',
  'Attachment handling is pragmatic: uploads are content-addressed, deduplicated per workspace, and prepared for pluggable storage drivers.',
  'The interface stays focused on conversation flow, with workspace navigation, threads, and search layered into the daily experience.',
];

const rolloutSteps = [
  {
    title: 'Deploy your stack',
    copy: 'Run the Go server, Postgres, search, and reverse proxy locally or in your preferred environment.',
  },
  {
    title: 'Create a workspace',
    copy: 'Invite the team, finish setup, and get everyone into a shared communication surface quickly.',
  },
  {
    title: 'Keep the conversation in-house',
    copy: 'Messages, files, and workspace configuration live in infrastructure you control instead of another seat-metered SaaS silo.',
  },
];

const faqs = [
  {
    question: 'Who is this for?',
    answer:
      'Small companies, internal teams, consultancies, and community groups that want a Slack or Teams style workflow without giving up hosting control.',
  },
  {
    question: 'What makes it different from a generic chat clone?',
    answer:
      'The product is being built around durable team workflows: workspace setup, authentication, search, attachments, threaded collaboration, and clean self-hosting boundaries.',
  },
  {
    question: 'Can I try the actual app?',
    answer:
      'Yes. The marketing site links directly into the existing sign-up and sign-in flows so you can move from overview to product without a separate stack.',
  },
];

export function MarketingPage(): ReactElement {
  return (
    <main className="marketing-page">
      <section className="marketing-hero">
        <div className="marketing-hero__backdrop" />
        <header className="marketing-nav">
          <Link to="/marketing" className="marketing-brand" aria-label="SliilS marketing home">
            <img src="/favicon.png" alt="" className="marketing-brand__icon" />
            <div className="marketing-brand__copy">
              <img src={logo} alt="SliilS" className="marketing-brand__wordmark" />
              <span>Self-hosted team collaboration</span>
            </div>
          </Link>

          <nav className="marketing-nav__links" aria-label="Marketing page sections">
            <a href="#features">Features</a>
            <a href="#how-it-works">How it works</a>
            <a href="#faq">FAQ</a>
          </nav>

          <div className="marketing-nav__actions">
            <Link to="/login" className="marketing-button marketing-button--ghost">
              Open app
            </Link>
            <Link to="/signup" className="marketing-button marketing-button--primary">
              Start a workspace
            </Link>
          </div>
        </header>

        <div className="marketing-hero__content">
          <div className="marketing-copy">
            <span className="marketing-eyebrow">Own the stack. Keep the conversation moving.</span>
            <h1>Team chat for small organizations that want control without the chaos.</h1>
            <p className="marketing-lead">
              SliilS is a self-hosted Slack or Teams alternative for teams that need channels,
              threads, search, attachments, and clean workspace boundaries without paying a
              per-seat tax forever.
            </p>
          </div>

          <div className="marketing-hero__actions">
            <div className="marketing-cta">
              <Link to="/signup" className="marketing-button marketing-button--primary">
                Create your workspace
              </Link>
              <Link to="/login" className="marketing-button marketing-button--secondary">
                Sign in to the app
              </Link>
            </div>
          </div>

          <MarketingMockup />

          <dl className="marketing-stats">
            <div>
              <dt>Team size</dt>
              <dd>2-50 people</dd>
            </div>
            <div>
              <dt>Deployment model</dt>
              <dd>Self-hosted</dd>
            </div>
            <div>
              <dt>Core focus</dt>
              <dd>Messaging + files</dd>
            </div>
          </dl>
        </div>
      </section>

      <section id="features" className="marketing-section">
        <div className="marketing-section__heading">
          <span className="marketing-kicker">Why teams pick it</span>
          <h2>Trim the suite, keep the collaboration surface your team actually uses.</h2>
          <p>
            The product is shaped around day-to-day coordination instead of bundling every
            possible office workflow into one heavyweight subscription.
          </p>
        </div>

        <div className="marketing-feature-grid">
          {featureCards.map((feature) => (
            <article key={feature.title} className="marketing-feature-card">
              <h3>{feature.title}</h3>
              <p>{feature.copy}</p>
            </article>
          ))}
        </div>
      </section>

      <section className="marketing-section marketing-section--split">
        <div className="marketing-panel marketing-panel--highlight">
          <span className="marketing-kicker">Practical product foundations</span>
          <h2>Made for operators who care where data lives and how the system behaves.</h2>
          <p>
            SliilS is already structured around a Go backend, React web client, shared schemas,
            search integration, and an attachment pipeline built for predictable storage behavior.
          </p>
        </div>

        <div className="marketing-panel marketing-panel--list">
          {operatingPrinciples.map((item) => (
            <div key={item} className="marketing-check">
              <span className="marketing-check__bullet" />
              <p>{item}</p>
            </div>
          ))}
        </div>
      </section>

      <section id="how-it-works" className="marketing-section">
        <div className="marketing-section__heading">
          <span className="marketing-kicker">How it works</span>
          <h2>Simple enough for a small team, structured enough for serious internal use.</h2>
        </div>

        <div className="marketing-steps">
          {rolloutSteps.map((step, index) => (
            <article key={step.title} className="marketing-step">
              <span className="marketing-step__index">0{index + 1}</span>
              <h3>{step.title}</h3>
              <p>{step.copy}</p>
            </article>
          ))}
        </div>
      </section>

      <section id="faq" className="marketing-section">
        <div className="marketing-section__heading">
          <span className="marketing-kicker">FAQ</span>
          <h2>Questions teams usually ask before they bring chat back in-house.</h2>
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

      <section className="marketing-section">
        <div className="marketing-cta-panel">
          <div>
            <span className="marketing-kicker">Ready when you are</span>
            <h2>See the product, start a workspace, and keep your collaboration stack yours.</h2>
          </div>
          <div className="marketing-cta">
            <Link to="/signup" className="marketing-button marketing-button--primary">
              Launch sign up
            </Link>
            <Link to="/login" className="marketing-button marketing-button--secondary">
              Go to sign in
            </Link>
          </div>
        </div>
      </section>
    </main>
  );
}
