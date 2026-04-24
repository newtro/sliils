import { useEffect, useState } from 'react';
import type { ReactElement } from 'react';
import { fetchFileAsBlobURL } from '../api/files';
import type { FileDTO } from '../api/files';
import { isCollaboraEditable } from '../api/collabora';
import { CollaboraOverlay } from './CollaboraOverlay';

interface Props {
  attachments: readonly FileDTO[];
}

/**
 * Renders attachments below a message. Images load via authenticated
 * fetch → blob URL → <img>. Non-images render as a file card with a
 * download link that triggers the same authenticated fetch.
 */
export function MessageAttachments({ attachments }: Props): ReactElement | null {
  if (attachments.length === 0) return null;
  return (
    <div className="sl-attachments">
      {attachments.map((a) =>
        a.mime.startsWith('image/') ? (
          <ImagePreview key={a.id} file={a} />
        ) : (
          <FileCard key={a.id} file={a} />
        ),
      )}
    </div>
  );
}

function ImagePreview({ file }: { file: FileDTO }): ReactElement {
  const [url, setUrl] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    let revoke: string | null = null;
    let cancelled = false;
    (async () => {
      try {
        const objURL = await fetchFileAsBlobURL(file.url);
        if (cancelled) {
          URL.revokeObjectURL(objURL);
          return;
        }
        revoke = objURL;
        setUrl(objURL);
      } catch (err) {
        if (!cancelled) setError((err as Error).message);
      }
    })();
    return () => {
      cancelled = true;
      if (revoke) URL.revokeObjectURL(revoke);
    };
  }, [file.url]);

  const maxInlineWidth = 360;
  const ratio = file.width && file.height ? file.width / file.height : 1;
  const w = file.width && file.width > maxInlineWidth ? maxInlineWidth : file.width ?? maxInlineWidth;
  const h = file.width && file.width > maxInlineWidth ? Math.round(maxInlineWidth / ratio) : file.height;

  return (
    <div className="sl-attach-image" style={{ maxWidth: maxInlineWidth }}>
      {error && <div className="sl-attach-error">Couldn&rsquo;t load {file.filename}</div>}
      {!error && url && (
        <img
          src={url}
          alt={file.filename}
          width={w}
          height={h}
          loading="lazy"
        />
      )}
      {!error && !url && (
        <div className="sl-attach-image-skeleton" style={{ width: w, height: h ?? 200 }} />
      )}
      <div className="sl-attach-image-caption">{file.filename}</div>
    </div>
  );
}

function FileCard({ file }: { file: FileDTO }): ReactElement {
  const [editorOpen, setEditorOpen] = useState(false);
  const collaboraReady = isCollaboraEditable(file.mime, file.filename);

  async function download() {
    try {
      const objURL = await fetchFileAsBlobURL(file.url);
      const a = document.createElement('a');
      a.href = objURL;
      a.download = file.filename;
      document.body.appendChild(a);
      a.click();
      a.remove();
      setTimeout(() => URL.revokeObjectURL(objURL), 60_000);
    } catch (err) {
      console.warn('download failed', err);
    }
  }

  return (
    <>
      <div className="sl-attach-card-wrap" style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
        <button type="button" className="sl-attach-card" onClick={download} title={`Download ${file.filename}`}>
          <div className="sl-attach-card-icon" aria-hidden="true">
            <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" />
              <polyline points="14 2 14 8 20 8" />
            </svg>
          </div>
          <div className="sl-attach-card-meta">
            <div className="sl-attach-card-name">{file.filename}</div>
            <div className="sl-attach-card-sub">
              {humanSize(file.size_bytes)} · {file.mime.split('/')[1]?.toUpperCase() || file.mime}
            </div>
          </div>
        </button>
        {collaboraReady && (
          <button
            type="button"
            className="sl-attach-edit"
            onClick={() => setEditorOpen(true)}
            title="Open in editor"
            style={{
              padding: '6px 10px',
              border: '1px solid #ccd8ff',
              background: '#f0f5ff',
              color: '#2a4ea4',
              borderRadius: 4,
              cursor: 'pointer',
              fontSize: 12,
            }}
          >
            Open in editor
          </button>
        )}
      </div>
      {editorOpen && (
        <CollaboraOverlay fileID={file.id} filename={file.filename} onClose={() => setEditorOpen(false)} />
      )}
    </>
  );
}

function humanSize(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}
