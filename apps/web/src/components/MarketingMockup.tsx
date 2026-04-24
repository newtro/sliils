import type { ReactElement } from 'react';

const channelItems = ['company', 'product', 'ops', 'launch-week'];
const threadItems = [
  'Attachment previews render inline without leaking auth tokens.',
  'Mentions, threads, and search keep conversations findable.',
  'Small teams get one place to coordinate without enterprise suite overhead.',
];

export function MarketingMockup(): ReactElement {
  return (
    <div className="marketing-mockup" aria-hidden="true">
      <div className="marketing-mockup__sidebar">
        <div className="marketing-mockup__workspace">
          <span className="marketing-mockup__workspace-badge">S</span>
          <div>
            <strong>SliilS</strong>
            <span>Launch workspace</span>
          </div>
        </div>

        <div className="marketing-mockup__group">
          <span className="marketing-mockup__label">Channels</span>
          {channelItems.map((channel) => (
            <div key={channel} className="marketing-mockup__channel">
              <span>#</span>
              <span>{channel}</span>
            </div>
          ))}
        </div>

        <div className="marketing-mockup__presence">
          <span className="marketing-mockup__label">Today</span>
          <div className="marketing-mockup__presence-card">
            <strong>4 teammates online</strong>
            <span>2 async check-ins waiting</span>
          </div>
        </div>
      </div>

      <div className="marketing-mockup__main">
        <div className="marketing-mockup__topbar">
          <div className="marketing-mockup__channel-summary">
            <strong># launch-week</strong>
            <span>Shipping without the per-seat tax</span>
          </div>
          <div className="marketing-mockup__pills">
            <span>search</span>
            <span>files</span>
            <span>threads</span>
          </div>
        </div>

        <div className="marketing-mockup__conversation">
          <article className="marketing-message marketing-message--accent">
            <div className="marketing-message__meta">
              <strong>Mina</strong>
              <span>09:18</span>
            </div>
            <p>Upload is live in staging. Duplicate files now collapse onto the same stored blob.</p>
          </article>

          <article className="marketing-message">
            <div className="marketing-message__meta">
              <strong>Jordan</strong>
              <span>09:20</span>
            </div>
            <p>
              Perfect. That keeps storage costs predictable and makes the media pipeline easier to
              reason about.
            </p>
            <div className="marketing-file-card">
              <span className="marketing-file-card__icon">PNG</span>
              <div className="marketing-file-card__meta">
                <strong>launch-board.png</strong>
                <span>Preview available inline</span>
              </div>
            </div>
          </article>

          <article className="marketing-message">
            <div className="marketing-message__meta">
              <strong>Alex</strong>
              <span>09:24</span>
            </div>
            <p>Threaded reply started for rollout notes.</p>
          </article>
        </div>
      </div>

      <aside className="marketing-mockup__thread">
        <div className="marketing-mockup__thread-header">
          <strong>Launch thread</strong>
          <span>3 replies</span>
        </div>
        {threadItems.map((item) => (
          <div key={item} className="marketing-mockup__thread-item">
            {item}
          </div>
        ))}
      </aside>
    </div>
  );
}
