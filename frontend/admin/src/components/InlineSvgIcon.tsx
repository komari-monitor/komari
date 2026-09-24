import { useEffect, useState, type CSSProperties } from "react";

const MAX_INLINE_SVG_BYTES = 64 * 1024;
const BLOCKED_ELEMENTS = new Set([
  "canvas",
  "embed",
  "foreignobject",
  "iframe",
  "link",
  "object",
  "script",
  "video",
]);

interface InlineSvgIconProps {
  src: string;
  alt: string;
  className?: string;
  style?: CSSProperties;
  loading?: "eager" | "lazy";
}

const isSvgUrl = (src: string) => /\.svg$/i.test(src.split(/[?#]/, 1)[0]);

const hasUnsafeReference = (value: string) =>
  /(?:javascript:|data:text\/html|url\(\s*(?!#))/i.test(value);

const sanitizeSvg = (source: string): string | null => {
  const document = new DOMParser().parseFromString(source, "image/svg+xml");
  const root = document.documentElement;
  if (
    !root ||
    root.localName?.toLowerCase() !== "svg" ||
    document.querySelector("parsererror")
  ) {
    return null;
  }

  const elements = [root, ...Array.from(root.querySelectorAll("*"))];
  for (const element of elements) {
    const tagName = element.localName?.toLowerCase();
    if (tagName && BLOCKED_ELEMENTS.has(tagName)) {
      element.remove();
      continue;
    }
    if (
      tagName === "style" &&
      hasUnsafeReference(element.textContent || "")
    ) {
      element.remove();
      continue;
    }

    for (const attribute of Array.from(element.attributes)) {
      const name = attribute.name.toLowerCase();
      const value = attribute.value.trim();
      if (name.startsWith("on") || name === "src") {
        element.removeAttribute(attribute.name);
        continue;
      }
      if (name === "href" || name === "xlink:href") {
        if (!value.startsWith("#")) element.removeAttribute(attribute.name);
        continue;
      }
      if (hasUnsafeReference(value)) {
        element.removeAttribute(attribute.name);
      }
    }
  }

  root.setAttribute("width", "100%");
  root.setAttribute("height", "100%");
  root.style.display = "block";
  root.style.width = "100%";
  root.style.height = "100%";
  return root.outerHTML;
};

const readResponseText = async (response: Response) => {
  const contentLength = Number(response.headers.get("content-length"));
  if (Number.isFinite(contentLength) && contentLength > MAX_INLINE_SVG_BYTES) {
    throw new Error("SVG icon is too large to inline");
  }
  if (!response.body) throw new Error("SVG icon response has no body");

  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let totalBytes = 0;
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) break;
      totalBytes += value.byteLength;
      if (totalBytes > MAX_INLINE_SVG_BYTES) {
        await reader.cancel();
        throw new Error("SVG icon is too large to inline");
      }
      chunks.push(value);
    }
  } finally {
    reader.releaseLock();
  }

  const bytes = new Uint8Array(totalBytes);
  let offset = 0;
  for (const chunk of chunks) {
    bytes.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return new TextDecoder().decode(bytes);
};

export default function InlineSvgIcon({
  src,
  alt,
  className,
  style,
  loading,
}: InlineSvgIconProps) {
  const [inlineSvg, setInlineSvg] = useState<{
    src: string;
    markup: string;
  } | null>(null);

  useEffect(() => {
    if (!isSvgUrl(src)) {
      setInlineSvg(null);
      return;
    }

    const controller = new AbortController();
    setInlineSvg(null);
    const loadSvg = async () => {
      try {
        const response = await fetch(src, { signal: controller.signal });
        if (!response.ok) throw new Error(`Failed to load SVG icon: ${response.status}`);
        const markup = sanitizeSvg(await readResponseText(response));
        if (!markup) throw new Error("Invalid SVG icon");
        if (!controller.signal.aborted) setInlineSvg({ src, markup });
      } catch {
        if (!controller.signal.aborted) setInlineSvg(null);
      }
    };
    void loadSvg();
    return () => controller.abort();
  }, [src]);

  if (inlineSvg?.src === src) {
    return (
      <span
        className={className}
        style={{ display: "inline-flex", ...style }}
        role={alt ? "img" : undefined}
        aria-label={alt || undefined}
        aria-hidden={alt ? undefined : true}
      >
        <span
          style={{ display: "inline-flex", width: "100%", height: "100%" }}
          dangerouslySetInnerHTML={{ __html: inlineSvg.markup }}
        />
      </span>
    );
  }

  return <img src={src} alt={alt} className={className} style={style} loading={loading} />;
}
