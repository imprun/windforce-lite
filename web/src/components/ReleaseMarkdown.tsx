import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { forgeRawFileURL, forgeTreeURL } from "../lib/repo";

type ReleaseMarkdownProps = {
  markdown: string;
  repoURL: string;
  commit: string;
  subpath: string;
};

function releaseFilePath(subpath: string, target: string): string | null {
  if (/^[a-z][a-z0-9+.-]*:/iu.test(target) || target.startsWith("//") || target.startsWith("#")) return null;
  const cleanTarget = target.replace(/^[./]+/, "");
  if (!cleanTarget || cleanTarget.startsWith("..")) return null;
  const cleanSubpath = subpath.replace(/^\/+|\/+$/g, "");
  return cleanSubpath ? `${cleanSubpath}/${cleanTarget}` : cleanTarget;
}

function safeAbsoluteURL(target: string, image: boolean): string | undefined {
  if (target.startsWith("//")) return undefined;
  const scheme = target.match(/^([a-z][a-z0-9+.-]*):/iu)?.[1].toLowerCase();
  if (!scheme) return target;
  if (scheme === "https" || scheme === "http" || (!image && scheme === "mailto")) return target;
  return undefined;
}

export function ReleaseMarkdown({ markdown, repoURL, commit, subpath }: ReleaseMarkdownProps) {
  function linkTarget(href: string | undefined): string | undefined {
    if (!href) return href;
    const path = releaseFilePath(subpath, href);
    return path ? forgeTreeURL(repoURL, commit, path) || href : safeAbsoluteURL(href, false);
  }

  function imageTarget(src: string | undefined): string | undefined {
    if (!src) return src;
    const path = releaseFilePath(subpath, src);
    return path ? forgeRawFileURL(repoURL, commit, path) || src : safeAbsoluteURL(src, true);
  }

  return (
    <div className="releaseMarkdown">
      <Markdown
        remarkPlugins={[remarkGfm]}
        components={{
          a: ({ href, children }) => (
            <a href={linkTarget(href)} target="_blank" rel="noreferrer">
              {children}
            </a>
          ),
          img: ({ src, alt }) => <img src={imageTarget(src)} alt={alt || ""} loading="lazy" />,
        }}
      >
        {markdown}
      </Markdown>
    </div>
  );
}
