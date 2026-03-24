(function initAssistantFactory() {
  function escapeHtml(value) {
    return String(value)
      .replaceAll("&", "&amp;")
      .replaceAll("<", "&lt;")
      .replaceAll(">", "&gt;")
      .replaceAll("\"", "&quot;")
      .replaceAll("'", "&#39;");
  }

  function sanitizeLinkHref(rawHref) {
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
    } catch (error) {
      return "";
    }
  }

  function renderInlineMarkdown(text) {
    const inlineTokens = [];
    const tokenized = String(text).replace(/`([^`\n]+)`/g, (_, code) => {
      const idx = inlineTokens.push(`<code class="assistant-md-inline-code">${escapeHtml(code)}</code>`) - 1;
      return `@@INLINE_${idx}@@`;
    });

    let html = escapeHtml(tokenized);
    html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, (full, label, href) => {
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

    return html.replace(/@@INLINE_(\d+)@@/g, (_, idx) => inlineTokens[Number(idx)] || "");
  }

  function parseTableRow(line) {
    return String(line)
      .trim()
      .replace(/^\|/, "")
      .replace(/\|$/, "")
      .split("|")
      .map((cell) => cell.trim());
  }

  function isTableDelimiterLine(line) {
    const cells = parseTableRow(line);
    if (cells.length === 0) {
      return false;
    }
    return cells.every((cell) => /^:?-{3,}:?$/.test(cell));
  }

  function tableAlignFromDelimiterCell(cell) {
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

  function renderMarkdown(text) {
    const fenceTokens = [];
    const normalized = String(text || "").replaceAll("\r\n", "\n");
    const fencedTokenized = normalized.replace(/```([^\n`]*)\n([\s\S]*?)```/g, (_, rawLang, code) => {
      const language = String(rawLang || "").trim();
      const langAttr = language ? ` data-lang="${escapeHtml(language)}"` : "";
      const html = `<pre class="assistant-md-pre"><code class="assistant-md-code"${langAttr}>${escapeHtml(code)}</code></pre>`;
      const idx = fenceTokens.push(html) - 1;
      return `@@FENCE_${idx}@@`;
    });

    const lines = fencedTokenized.split("\n");
    const blocks = [];
    let paragraphLines = [];
    let listType = "";
    let listItems = [];

    function flushParagraph() {
      if (paragraphLines.length === 0) {
        return;
      }
      const paragraphHtml = paragraphLines.map((line) => renderInlineMarkdown(line)).join("<br>");
      blocks.push(`<p class="assistant-md-p">${paragraphHtml}</p>`);
      paragraphLines = [];
    }

    function flushList() {
      if (!listType || listItems.length === 0) {
        listType = "";
        listItems = [];
        return;
      }
      const items = listItems.map((item) => `<li>${renderInlineMarkdown(item)}</li>`).join("");
      const cls = listType === "ol" ? "assistant-md-ol" : "assistant-md-ul";
      blocks.push(`<${listType} class="${cls}">${items}</${listType}>`);
      listType = "";
      listItems = [];
    }

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
        const rows = [];
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
        const itemText = ulMatch ? ulMatch[1] : olMatch[1];
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

  function sleep(ms) {
    return new Promise((resolve) => {
      setTimeout(resolve, ms);
    });
  }

  function createAssistantChat(options = {}) {
    const chatEndpoint = options.chatEndpoint || "/api/v1/assistant/chat";
    const openBtn = document.getElementById(options.openButtonId || "assistant-open");
    const modalEl = document.getElementById(options.modalId || "assistant-chat-modal");
    const bodyEl = document.getElementById(options.bodyId || "assistant-chat-body");
    const closeBtn = document.getElementById(options.closeButtonId || "assistant-chat-close");
    const formEl = document.getElementById(options.formId || "assistant-chat-form");
    const inputEl = document.getElementById(options.inputId || "assistant-chat-input");
    const sendBtn = document.getElementById(options.sendButtonId || "assistant-chat-send");

    if (!openBtn || !modalEl || !bodyEl || !closeBtn || !formEl || !inputEl || !sendBtn) {
      return {
        setEnabled() {},
      };
    }

    const messages = [];
    let conversationID = "";
    let activeRequestID = "";
    let pending = false;

    function showModal() {
      modalEl.classList.remove("hidden");
      modalEl.setAttribute("aria-hidden", "false");
    }

    function hideModal() {
      modalEl.classList.add("hidden");
      modalEl.setAttribute("aria-hidden", "true");
    }

    function setPending(nextPending) {
      pending = nextPending;
      sendBtn.disabled = nextPending;
      inputEl.disabled = nextPending;
    }

    function setEnabled(enabled) {
      if (enabled) {
        openBtn.classList.remove("hidden");
        return;
      }

      openBtn.classList.add("hidden");
      hideModal();
    }

    function renderMessages() {
      if (messages.length === 0) {
        bodyEl.innerHTML = `<p class="meta">Assistant is ready.</p>`;
        return;
      }

      bodyEl.innerHTML = `
        <div class="assistant-chat-list">
          ${messages
            .map((message) => {
              const roleClass = `assistant-chat-message-${message.role}`;
              const roleLabel = message.role === "user" ? "You" : message.role === "assistant" ? "Assistant" : "System";
              const messageHtml = message.role === "assistant"
                ? `<div class="assistant-chat-text assistant-chat-markdown">${renderMarkdown(message.text)}</div>`
                : `<p class="assistant-chat-text">${escapeHtml(message.text)}</p>`;
              return `
                <article class="assistant-chat-message ${roleClass}">
                  <p class="assistant-chat-role">${escapeHtml(roleLabel)}</p>
                  ${messageHtml}
                </article>
              `;
            })
            .join("")}
        </div>
      `;
      bodyEl.scrollTop = bodyEl.scrollHeight;
    }

    function pushMessage(role, text) {
      messages.push({
        role,
        text,
      });
      renderMessages();
    }

    async function requestAssistant(payload) {
      const response = await fetch(chatEndpoint, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      });
      if (!response.ok) {
        throw new Error(`HTTP ${response.status}`);
      }

      return response.json();
    }

    async function runAssistantMessage(message) {
      let payload = {
        conversation_id: conversationID || undefined,
        message,
        wait_timeout_ms: 12000,
      };

      for (let attempt = 0; attempt < 30; attempt += 1) {
        const response = await requestAssistant(payload);
        conversationID = response.conversation_id || conversationID;
        activeRequestID = response.request_id || activeRequestID;

        if (response.status === "in_progress") {
          const delay = Number(response.poll_after_ms) > 0 ? Number(response.poll_after_ms) : 1000;
          payload = {
            conversation_id: conversationID || undefined,
            request_id: activeRequestID || undefined,
            wait_timeout_ms: 12000,
          };
          await sleep(delay);
          continue;
        }

        activeRequestID = "";
        if (response.status === "completed") {
          const answer = response.answer || "Assistant returned empty answer.";
          pushMessage("assistant", answer);
          return;
        }

        if (response.status === "disabled") {
          setEnabled(false);
        }

        pushMessage("system", response.error_message || `Assistant status: ${response.status}`);
        return;
      }

      activeRequestID = "";
      pushMessage("system", "Assistant request timeout. Try again.");
    }

    async function submitMessage(event) {
      event.preventDefault();
      if (pending) {
        return;
      }

      const message = inputEl.value.trim();
      if (!message) {
        return;
      }

      inputEl.value = "";
      pushMessage("user", message);
      setPending(true);
      try {
        await runAssistantMessage(message);
      } catch (err) {
        activeRequestID = "";
        pushMessage("system", `Assistant failed: ${err.message}`);
      } finally {
        setPending(false);
        inputEl.focus();
      }
    }

    openBtn.addEventListener("click", () => {
      showModal();
      inputEl.focus();
    });
    closeBtn.addEventListener("click", hideModal);
    modalEl.addEventListener("click", (event) => {
      const target = event.target;
      if (target instanceof HTMLElement && target.dataset.closeAssistant === "true") {
        hideModal();
      }
    });
    formEl.addEventListener("submit", submitMessage);
    document.addEventListener("keydown", (event) => {
      if (event.key === "Escape" && !modalEl.classList.contains("hidden")) {
        hideModal();
      }
    });

    renderMessages();

    return {
      setEnabled,
    };
  }

  window.createAssistantChat = createAssistantChat;
}());
