const { test } = require("node:test");
const assert = require("node:assert/strict");
const { parse } = require("../web/message-text.js");
test("recognizes URLs, email and phone while preserving every original character", () => {
  const text =
    "网址：https://example.com/a?q=1，www.example.org。邮箱 alice+test@example.com 电话13800138000 010-12345678 +1 (415) 555-2671";
  const parts = parse(text);
  assert.equal(parts.map((x) => x.text).join(""), text);
  assert.deepEqual(
    parts.filter((x) => x.type !== "text").map((x) => x.type),
    ["url", "url", "email", "phone", "phone", "phone"],
  );
  assert.equal(
    parts.find((x) => x.type === "email").href,
    "mailto:alice%2Btest@example.com",
  );
  assert.ok(parts.some((x) => x.href === "tel:13800138000"));
});
test("handles brackets, private addresses, IPv6 and punctuation without HTML parsing", () => {
  const text =
    "(http://192.168.1.2:8080/path). https://[::1]:8080/a https://example.com/a_(b)";
  const parts = parse(text);
  assert.equal(parts.map((x) => x.text).join(""), text);
  assert.deepEqual(
    parts.filter((x) => x.type === "url").map((x) => x.text),
    [
      "http://192.168.1.2:8080/path",
      "https://[::1]:8080/a",
      "https://example.com/a_(b)",
    ],
  );
});
test("dangerous schemes, credentials, dates and numeric IDs stay plain", () => {
  for (const text of [
    "javascript:alert(1)",
    "data:text/html,<script>alert(1)</script>",
    "file:///tmp/a",
    "javascript:https://evil.example",
    "https://user:password@example.com",
    "订单12313800138000123",
    "2026-11-13",
    "1234567890",
  ])
    assert.equal(
      parse(text).some((x) => x.type !== "text"),
      false,
      text,
    );
});
test("bounds link count and long malformed text without losing contents", () => {
  const text = "https://example.com ".repeat(200);
  const parts = parse(text);
  assert.equal(parts.filter((x) => x.type === "url").length, 128);
  assert.equal(parts.map((x) => x.text).join(""), text);
  const long = "a".repeat(16384);
  assert.equal(
    parse(long)
      .map((x) => x.text)
      .join(""),
    long,
  );
});
