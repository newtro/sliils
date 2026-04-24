import type { ComponentPropsWithoutRef, ReactElement, ReactNode } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkBreaks from 'remark-breaks';
import remarkGfm from 'remark-gfm';

import type { WorkspaceMember } from '../api/workspaces';

interface Props {
  body: string;
  members: readonly WorkspaceMember[];
  currentUserID?: number;
}

/**
 * Renders a message body as Markdown with GFM (tables, task lists,
 * autolinks, strikethrough, fenced code) and replaces `<@N>` mention tokens
 * with styled badges.
 *
 * Strategy:
 *   1. Rewrite raw `<@N>` tokens into a literal placeholder string that
 *      survives Markdown parsing (we avoid choosing a syntax that would be
 *      further transformed — no stars, no brackets).
 *   2. Pass the result to react-markdown. The parser and all links /
 *      images render through React (no dangerouslySetInnerHTML).
 *   3. In our text-node renderer, split placeholders back into Mention
 *      components so mentions work even inside formatted text (bold, list
 *      items, table cells, etc.).
 *
 * Security: react-markdown does NOT execute raw HTML; we also don't pass
 * rehype-raw. Unknown protocols on links are stripped by the default
 * uriTransformer. That's safe by default.
 */
export function MessageBody({ body, members, currentUserID }: Props): ReactElement {
  const prepared = encodeMentions(body);

  return (
    <div className="sl-md">
      <ReactMarkdown
        // remark-breaks turns single newlines into <br>, matching Slack /
        // Teams behavior. Without it, `foo\nbar` would render as `foo bar`.
        remarkPlugins={[remarkGfm, remarkBreaks]}
        components={{
          // Open links in a new tab. The default uriTransformer already
          // blocks javascript:/data:/vbscript:, so this is purely UX.
          // Destructure `node` (react-markdown's AST reference) so it
          // doesn't spread onto the DOM element as an attribute.
          a: ({ node: _n, href, children, ...rest }) => (
            <a href={href} target="_blank" rel="noreferrer noopener" {...rest}>
              {children}
            </a>
          ),
          img: ({ node: _n, alt, src, ...rest }) => (
            <img
              alt={alt ?? ''}
              src={src}
              loading="lazy"
              style={{ maxHeight: 320, maxWidth: '100%', borderRadius: 6 }}
              {...rest}
            />
          ),
          // Every string child passes through here, so we can swap mention
          // placeholders for badges without touching the parser internals.
          p: ({ node: _n, children, ...rest }) => (
            <p {...(rest as ComponentPropsWithoutRef<'p'>)}>
              {injectMentions(children, members, currentUserID)}
            </p>
          ),
          li: ({ node: _n, children, ...rest }) => (
            <li {...(rest as ComponentPropsWithoutRef<'li'>)}>
              {injectMentions(children, members, currentUserID)}
            </li>
          ),
          td: ({ node: _n, children, ...rest }) => (
            <td {...(rest as ComponentPropsWithoutRef<'td'>)}>
              {injectMentions(children, members, currentUserID)}
            </td>
          ),
          th: ({ node: _n, children, ...rest }) => (
            <th {...(rest as ComponentPropsWithoutRef<'th'>)}>
              {injectMentions(children, members, currentUserID)}
            </th>
          ),
        }}
      >
        {prepared}
      </ReactMarkdown>
    </div>
  );
}

// ---- mention helpers -----------------------------------------------------

// Placeholder sequence that (a) can't appear in real user text and
// (b) has no Markdown meaning so it survives parsing unchanged.
const MENTION_PREFIX = '⁣MENTION⁣';
const MENTION_SUFFIX = '⁣';
const MENTION_RE = /⁣MENTION⁣(\d+)⁣/g;

function encodeMentions(body: string): string {
  return body.replace(/<@(\d+)>/g, (_, id) => `${MENTION_PREFIX}${id}${MENTION_SUFFIX}`);
}

// Walks a react-markdown children array, splitting any string child that
// contains a mention placeholder and injecting Mention components in the
// gaps. Non-string children pass through unchanged.
function injectMentions(
  children: ReactNode,
  members: readonly WorkspaceMember[],
  currentUserID?: number,
): ReactNode {
  const arr = Array.isArray(children) ? children : [children];
  const out: ReactNode[] = [];
  let key = 0;
  for (const node of arr) {
    if (typeof node !== 'string') {
      out.push(node);
      continue;
    }
    let lastIndex = 0;
    MENTION_RE.lastIndex = 0;
    let m: RegExpExecArray | null;
    while ((m = MENTION_RE.exec(node)) !== null) {
      if (m.index > lastIndex) {
        out.push(node.slice(lastIndex, m.index));
      }
      const uid = Number(m[1]);
      const member = members.find((mem) => mem.user_id === uid);
      const isMe = currentUserID === uid;
      const label = member?.display_name || member?.email.split('@')[0] || `user ${uid}`;
      out.push(
        <span
          key={`mention-${key++}`}
          className={`sl-mention ${isMe ? 'sl-mention-me' : ''}`}
        >
          @{label}
        </span>,
      );
      lastIndex = m.index + m[0].length;
    }
    if (lastIndex < node.length) {
      out.push(node.slice(lastIndex));
    }
  }
  return out;
}
