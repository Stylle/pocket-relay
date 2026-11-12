"use strict";
// Pure text recognition; never interprets HTML or arbitrary URL schemes.
(function (root) {
  const candidates =
    /https?:\/\/[^\s<>"'`，。！？；、【】「」“”‘’]+|www\.[^\s<>"'`，。！？；、【】「」“”‘’]+|(?:mailto:)?[a-z0-9.!#$%&'*+\/=?^_`{|}~-]{1,64}@[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?\.[a-z]{2,24}|(?:\+86[ -]?)?1[3-9]\d{9}|0\d{2,3}[- ]\d{7,8}|\(\d{3}\)[ -]?\d{3}[- ]\d{4}|\+\d[\d ()-]{5,23}\d/gi;
  function trimURL(value) {
    value = value.replace(/[.,;!?:]+$/g, "");
    for (const [close, open] of [
      [")", "("],
      ["]", "["],
      ["}", "{"],
    ]) {
      while (
        value.endsWith(close) &&
        value.split(close).length > value.split(open).length
      )
        value = value.slice(0, -1);
    }
    return value;
  }
  function parse(text) {
    text = String(text);
    const out = [];
    let offset = 0,
      links = 0;
    for (const match of text.matchAll(new RegExp(candidates.source, "gi"))) {
      const start = match.index,
        raw = match[0];
      let value = raw,
        href = "",
        type = "";
      if (start > 0 && /[a-z0-9_@/:\\.+-]/i.test(text[start - 1])) continue;
      if (/^(https?:\/\/|www\.)/i.test(value)) {
        value = trimURL(value);
        try {
          const url = new URL(
            /^www\./i.test(value) ? "https://" + value : value,
          );
          if (
            ["http:", "https:"].includes(url.protocol) &&
            url.hostname &&
            !url.username &&
            !url.password
          ) {
            href = url.href;
            type = "url";
          }
        } catch {}
      } else if (value.includes("@")) {
        const address = value.replace(/^mailto:/i, "");
        const at = address.lastIndexOf("@"),
          local = address.slice(0, at),
          domain = address.slice(at + 1);
        if (
          !local.startsWith(".") &&
          !local.endsWith(".") &&
          !local.includes("..") &&
          domain
            .split(".")
            .every((label) => /^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?$/i.test(label))
        ) {
          href = "mailto:" + encodeURIComponent(local) + "@" + domain;
          type = "email";
        }
      } else {
        const digits = value.replace(/[^\d+]/g, "");
        if (
          !/\d/.test(text[start + raw.length] || "") &&
          /^\+?\d{7,15}$/.test(digits) &&
          !/^\+?\d{4}-\d{2}-\d{2}$/.test(value)
        ) {
          href = "tel:" + digits;
          type = "phone";
        }
      }
      if (!href) continue;
      if (links++ >= 128) break;
      if (start > offset)
        out.push({ type: "text", text: text.slice(offset, start) });
      out.push({ type, text: value, href });
      offset = start + value.length;
    }
    if (offset < text.length)
      out.push({ type: "text", text: text.slice(offset) });
    return out;
  }
  root.RelayMessageText = { parse };
  if (typeof module !== "undefined") module.exports = { parse };
})(typeof window === "undefined" ? globalThis : window);
