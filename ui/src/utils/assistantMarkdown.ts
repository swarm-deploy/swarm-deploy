import { escapeHtml } from "./escape";

function sanitizeLinkHref(rawHref: string): string {
  const trimmed = String(rawHref || "").trim();
  if (!trimmed) {
    return "";
  }

  const hrefWithNoTitle = trimmed.split(/\s+/)[0].replace(/^<|>$/g, "");
  if (!hrefWithNoTitle) {
    return "";
  }

  try {
    const parsed = new URL(hrefWithNoTitle, window.location.origin);
    const protocol = parsed.protocol.toLowerCase();
    if (protocol !== "http:" && protocol !== "https:" && protocol !== "mailto:") {
      return "";
    }
    return parsed.href;
  } catch {
    return "";
  }
}

function renderInlineMarkdown(text: string): string {
  const inlineTokens: string[] = [];
  const tokenized = String(text).replace(/`([^`\n]+)`/g, (_, code: string) => {
    const idx = inlineTokens.push(`<code class="assistant-md-inline-code">${escapeHtml(code)}</code>`) - 1;
    return `@@INLINE_${idx}@@`;
  });

  let html = escapeHtml(tokenized);
  html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (full, label: string, href: string) => {
    const safeHref = sanitizeLinkHref(href);
    if (!safeHref) {
      return full;
    }

    const escapedHref = escapeHtml(safeHref);
    const escapedLabel = escapeHtml(label);
    const isExternal = safeHref.startsWith("http://") || safeHref.startsWith("https://");
    const externalAttrs = isExternal ? " target=\"_blank\" rel=\"noopener noreferrer\"" : "";
    const idx = inlineTokens.push(
      `<a class="assistant-md-link" href="${escapedHref}"${externalAttrs}>${escapedLabel}</a>`,
    ) - 1;
    return `@@INLINE_${idx}@@`;
  });

  html = html.replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/\*([^*]+)\*/g, "<em>$1</em>");

  return html.replace(/@@INLINE_(\d+)@@/g, (_, idx: string) => inlineTokens[Number(idx)] || "");
}

function parseTableRow(line: string): string[] {
  return String(line)
    .trim()
    .replace(/^\|/, "")
    .replace(/\|$/, "")
    .split("|")
    .map((cell) => cell.trim());
}

function isTableDelimiterLine(line: string): boolean {
  const cells = parseTableRow(line);
  if (cells.length === 0) {
    return false;
  }

  return cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}

function tableAlignFromDelimiterCell(cell: string): string {
  const trimmed = String(cell).trim();
  const starts = trimmed.startsWith(":");
  const ends = trimmed.endsWith(":");
  if (starts && ends) {
    return "center";
  }
  if (ends) {
    return "right";
  }
  if (starts) {
    return "left";
  }
  return "";
}

export function renderAssistantMarkdown(text: string): string {
  const fenceTokens: string[] = [];
  const normalized = String(text || "").replaceAll("\r\n", "\n");
  const fencedTokenized = normalized.replace(/```([^\n`]*)\n([\s\S]*?)```/g, (_, rawLang: string, code: string) => {
    const language = String(rawLang || "").trim();
    const langAttr = language ? ` data-lang="${escapeHtml(language)}"` : "";
    const html = `<pre class="assistant-md-pre"><code class="assistant-md-code"${langAttr}>${escapeHtml(code)}</code></pre>`;
    const idx = fenceTokens.push(html) - 1;
    return `@@FENCE_${idx}@@`;
  });

  const lines = fencedTokenized.split("\n");
  const blocks: string[] = [];
  let paragraphLines: string[] = [];
  let listType = "";
  let listItems: string[] = [];

  const flushParagraph = () => {
    if (paragraphLines.length === 0) {
      return;
    }

    const paragraphHtml = paragraphLines.map((line) => renderInlineMarkdown(line)).join("<br>");
    blocks.push(`<p class="assistant-md-p">${paragraphHtml}</p>`);
    paragraphLines = [];
  };

  const flushList = () => {
    if (!listType || listItems.length === 0) {
      listType = "";
      listItems = [];
      return;
    }

    const items = listItems.map((item) => `<li>${renderInlineMarkdown(item)}</li>`).join("");
    const className = listType === "ol" ? "assistant-md-ol" : "assistant-md-ul";
    blocks.push(`<${listType} class="${className}">${items}</${listType}>`);
    listType = "";
    listItems = [];
  };

  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i];
    const trimmed = line.trim();
    const fenceMatch = trimmed.match(/^@@FENCE_(\d+)@@$/);

    if (fenceMatch) {
      flushParagraph();
      flushList();
      blocks.push(fenceTokens[Number(fenceMatch[1])] || "");
      continue;
    }

    if (!trimmed) {
      flushParagraph();
      flushList();
      continue;
    }

    if (i + 1 < lines.length && line.includes("|") && isTableDelimiterLine(lines[i + 1])) {
      flushParagraph();
      flushList();

      const headerCells = parseTableRow(line);
      const delimiterCells = parseTableRow(lines[i + 1]);
      const alignments = delimiterCells.map((cell) => tableAlignFromDelimiterCell(cell));
      const rows: string[][] = [];
      i += 2;
      while (i < lines.length && lines[i].trim() && lines[i].includes("|")) {
        rows.push(parseTableRow(lines[i]));
        i += 1;
      }
      i -= 1;

      const headHtml = headerCells
        .map((cell, idx) => {
          const align = alignments[idx] ? ` style="text-align:${alignments[idx]}"` : "";
          return `<th${align}>${renderInlineMarkdown(cell)}</th>`;
        })
        .join("");

      const bodyHtml = rows
        .map((row) => {
          const rowHtml = row
            .map((cell, idx) => {
              const align = alignments[idx] ? ` style="text-align:${alignments[idx]}"` : "";
              return `<td${align}>${renderInlineMarkdown(cell)}</td>`;
            })
            .join("");
          return `<tr>${rowHtml}</tr>`;
        })
        .join("");

      blocks.push(
        `<div class="assistant-md-table-wrap"><table class="assistant-md-table"><thead><tr>${headHtml}</tr></thead><tbody>${bodyHtml}</tbody></table></div>`,
      );
      continue;
    }

    const headingMatch = line.match(/^(#{1,6})\s+(.+)$/);
    if (headingMatch) {
      flushParagraph();
      flushList();
      const level = headingMatch[1].length;
      blocks.push(`<h${level} class="assistant-md-h${level}">${renderInlineMarkdown(headingMatch[2])}</h${level}>`);
      continue;
    }

    const blockquoteMatch = line.match(/^>\s?(.*)$/);
    if (blockquoteMatch) {
      flushParagraph();
      flushList();
      blocks.push(`<blockquote class="assistant-md-blockquote">${renderInlineMarkdown(blockquoteMatch[1])}</blockquote>`);
      continue;
    }

    const ulMatch = line.match(/^[-*]\s+(.+)$/);
    const olMatch = line.match(/^\d+\.\s+(.+)$/);
    if (ulMatch || olMatch) {
      const nextType = ulMatch ? "ul" : "ol";
      const itemText = ulMatch ? ulMatch[1] : (olMatch as RegExpMatchArray)[1];
      flushParagraph();
      if (listType && listType !== nextType) {
        flushList();
      }
      listType = nextType;
      listItems.push(itemText);
      continue;
    }

    if (listType) {
      flushList();
    }
    paragraphLines.push(line);
  }

  flushParagraph();
  flushList();

  return blocks.join("");
}
