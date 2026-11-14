// DOM unit tests only. These do not claim real-browser or mobile visual QA.
const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const { JSDOM } = require("jsdom");
const html = fs.readFileSync("web/index.html", "utf8"),
  script = fs.readFileSync("web/app.js", "utf8");
const user = { id: "owner", name: "测试用户", admin: true, expires: 0 };
const settings = {
  public_registration: false,
  guest_hours: 1,
  max_file_mb: 256,
  quota_mb: 5120,
  device_template: "{device} · {ip}",
  active_store: "local",
  message_size: "medium",
};
async function settle() {
  for (let i = 0; i < 8; i++) await new Promise((r) => setImmediate(r));
}
function fixture(
  t,
  {
    setup = false,
    publicRegistration = false,
    messages = [],
    navigation = "navigate",
    sessionToken = "",
    autoLoadImages = true,
    remembered = false,
    revealText = "revealed test body",
    messageSize = "medium",
    language = false,
    restoreWait,
    restoreStatus,
    currentCount,
  } = {},
) {
  const dom = new JSDOM(html, {
    url: "http://localhost/",
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  t.after(() => dom.window.close());
  const w = dom.window;
  // jsdom does not implement native dialog lifecycle; real-browser QA checks it too.
  w.HTMLDialogElement.prototype.showModal = function () { this.open = true; };
  w.HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new w.Event("close")); };
  const calls = [];
  let tgConfig = {
    enabled: false,
    receive: true,
    send: true,
    include_concealed: false,
    user_id: "owner",
    chat_id: "",
  };
  let tgConfigured = false;
  let privacyRevision = 0;
  w.Headers = Headers;
  w.performance.getEntriesByType = () => [{ type: navigation }];
  if (sessionToken)
    w.sessionStorage.setItem(
      "pr-session",
      JSON.stringify({ token: sessionToken }),
    );
  w.setInterval = () => 0;
  w.confirm = () => true;
  w.fetch = async (path, opts = {}) => {
    calls.push({ path, opts });
    let value;
    if (path === "/api/session/restore") {
      if (restoreWait) await restoreWait;
      if (restoreStatus) return new Response(JSON.stringify({error:"restore temporarily unavailable"}),{status:restoreStatus});
      if (!remembered)
        return new Response(JSON.stringify({ error: "not remembered" }), {
          status: 401,
        });
      value = {
        token: "remembered-memory-token",
        user,
        remembered: true,
        idle_minutes: 0,
        expires_at: Date.now() + 7 * 86400000,
        server_time: Date.now(),
        last_active: Date.now(),
      };
    } else if (path === "/api/status")
      value = {
        setup,
        public_registration: publicRegistration,
        guest_hours: 1,
      };
    else if (path === "/api/login" || path === "/api/enroll/confirm")
      value = {
        token: "test-only-bearer",
        user,
        remembered: JSON.parse(opts.body).remember === true,
        idle_minutes: JSON.parse(opts.body).remember ? 0 : 15,
      };
    else if (path === "/api/session")
      value = {
        user,
        idle_minutes: 15,
        last_active: Date.now(),
        server_time: Date.now(),
      };
    else if (path === "/api/enroll")
      value = {
        enrollment: "test-enrollment",
        secret: "TESTONLYSECRET",
        qr: "data:image/png;base64,AA==",
      };
    else if (/^\/api\/messages\/[^/]+\/text$/.test(path))
      value = {
        text: revealText,
        privacy_version:
          messages.find((m) => path.includes(m.id))?.privacy_version ?? 0,
      };
    else if (path.endsWith("/privacy") && opts.method === "PUT") {
      const m = messages.find(
        (m) => path === "/api/messages/" + m.id + "/privacy",
      );
      m.concealed = JSON.parse(opts.body).concealed;
      m.privacy_version = (m.privacy_version ?? 0) + 1;
      privacyRevision++;
      value = {
        ok: true,
        privacy_version: m.privacy_version,
        privacy_revision: privacyRevision,
      };
    } else if (path.startsWith("/api/messages"))
      value =
        opts.method === "POST"
          ? { id: "new-message" }
          : {
              messages,
              current_count: currentCount,
              user,
              device_template: "{device} · {ip}",
              max_file_mb: 256,
              auto_load_images: autoLoadImages,
              message_size: messageSize,
              privacy_revision: privacyRevision,
            };
    else if (/^\/api\/files\/[^/]+\/ticket$/.test(path))
      value = { url: "/file/test?ticket=test-only" };
    else if (path === "/api/admin/telegram") {
      if (opts.method === "PUT") {
        const body = JSON.parse(opts.body);
        tgConfigured = tgConfigured || !!body.token;
        tgConfig = { ...body, token: "" };
        value = { ok: true };
      } else
        value = {
          config: tgConfig,
          configured: tgConfigured,
          revision: "test-revision",
          pending: 0,
          failed: 0,
        };
    } else if (path === "/api/admin/telegram/test")
      value = { username: "test_only_bot", chats: ["42"] };
    else if (path === "/api/admin")
      value =
        opts.method === "PUT"
          ? { ok: true }
          : {
              settings,
              stores: [{ id: "local", kind: "local", name: "本地" }],
              users: [user],
            };
    else if (path === "/api/logout") value = { ok: true };
    else value = { ok: true };
    return new Response(JSON.stringify(value), {
      status: 200,
      headers: { "content-type": "application/json" },
    });
  };
  if(language)w.eval(fs.readFileSync("web/ui-i18n.js", "utf8"));
  w.eval(fs.readFileSync("web/session.js", "utf8"));
  w.eval(fs.readFileSync("web/message-text.js", "utf8"));
  w.eval(fs.readFileSync("web/api.js", "utf8"));
  w.eval(
    script +
      "\nwindow.__test={state,setView,refresh,renderMessages,renderAttachments,browserSession,recordActivity,flushActivity,expireSession};",
  );
  return { w, $: (id) => w.document.getElementById(id), calls };
}
async function login(f) {
  f.$("account").value = "测试用户";
  f.$("code").value = "123456";
  f.$("auth-form").dispatchEvent(new f.w.Event("submit", { cancelable: true }));
  await settle();
  assert.equal(f.$("workspace").hidden, false);
}

test("today view requests local calendar boundaries across all account history", async (t) => {
  const f = fixture(t);
  await settle();
  await login(f);
  f.w.document.querySelector('[data-view="today"]').click();
  await settle();
  const call = f.calls.filter(c => c.path.startsWith('/api/messages?')).at(-1);
  const query = new URL(call.path, 'http://localhost').searchParams;
  const now = new Date();
  assert.equal(query.get('scope'), 'all');
  assert.equal(Number(query.get('from')), new Date(now.getFullYear(), now.getMonth(), now.getDate()).getTime());
  assert.equal(Number(query.get('to')), new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1).getTime());
  assert.equal(f.$('view-title').textContent, '今日消息');
  assert.equal(f.$('composer').hidden, false);
  assert.match(f.$('timeline').textContent, /今天还没有消息/);
  f.w.document.querySelector('[data-view="all"]').click();
  await settle();
  const all = new URL(f.calls.filter(c => c.path.startsWith('/api/messages?')).at(-1).path, 'http://localhost').searchParams;
  assert.equal(all.has('from'), false);
  assert.equal(all.has('to'), false);
});
test("login preserves refresh continuity without storing token in localStorage", async (t) => {
  const f = fixture(t);
  await settle();
  await login(f);
  assert.equal(f.w.localStorage.getItem("pr-account"), "测试用户");
  assert.equal(
    [...Array(f.w.localStorage.length)].some((_, i) =>
      f.w.localStorage
        .getItem(f.w.localStorage.key(i))
        .includes("test-only-bearer"),
    ),
    false,
  );
  f.w.dispatchEvent(new f.w.Event("pagehide"));
  assert.equal(f.$("workspace").hidden, false);
  assert.equal(
    JSON.parse(f.w.sessionStorage.getItem("pr-session")).token,
    "test-only-bearer",
  );
  f.$("logout").click();
  await settle();
  assert.equal(f.$("workspace").hidden, true);
  assert.equal(f.w.sessionStorage.getItem("pr-session"), null);
});
test("reload restores server-validated session, fresh navigation requires verification", async (t) => {
  const restored = fixture(t, {
    navigation: "reload",
    sessionToken: "test-only-bearer",
  });
  await settle();
  assert.equal(restored.$("workspace").hidden, false);
  assert.ok(restored.calls.some((c) => c.path === "/api/session"));
  assert.equal(
    restored.calls.some((c) => c.path === "/api/login"),
    false,
  );
  const fresh = fixture(t, {
    navigation: "navigate",
    sessionToken: "test-only-bearer",
  });
  await settle();
  assert.equal(fresh.$("workspace").hidden, true);
  assert.equal(fresh.w.sessionStorage.getItem("pr-session"), null);
});

test("remember checkbox is opt-in and remembered cookie restores a freshly opened page", async (t) => {
  const ordinary = fixture(t);
  await settle();
  assert.equal(ordinary.$("remember-login").checked, false);
  ordinary.$("remember-login").checked = true;
  await login(ordinary);
  const call = ordinary.calls.find((c) => c.path === "/api/login");
  assert.equal(JSON.parse(call.opts.body).remember, true);
  assert.equal(ordinary.w.__test.browserSession.remembered, true);
  const restored = fixture(t, { navigation: "navigate", remembered: true });
  await settle();
  assert.equal(restored.$("workspace").hidden, false);
  assert.equal(restored.w.__test.browserSession.remembered, true);
  const restoreCall = restored.calls.find(
    (c) => c.path === "/api/session/restore",
  );
  assert.equal(restoreCall.opts.method, "POST");
  assert.equal(restoreCall.opts.headers.get("X-Relay-Session"), "1");
  assert.equal(
    restored.calls.some((c) => c.path === "/api/login"),
    false,
  );
  assert.equal(restored.w.localStorage.getItem("pr-session"), null);
  const event = new restored.w.Event("pageshow");
  Object.defineProperty(event, "persisted", { value: true });
  restored.w.dispatchEvent(event);
  assert.equal(restored.$("workspace").hidden, false);
  restored.$("logout").click();
  await settle();
  assert.equal(restored.$("workspace").hidden, true);
});
test("idle expiry and back-forward cache restore lock the UI", async (t) => {
  const f = fixture(t);
  await settle();
  await login(f);
  f.w.__test.browserSession.lastActive = Date.now() - 16 * 60000;
  f.w.__test.recordActivity({ isTrusted: true });
  assert.equal(f.$("workspace").hidden, true);
  assert.equal(f.w.sessionStorage.getItem("pr-session"), null);
  await login(f);
  const event = new f.w.Event("pageshow");
  Object.defineProperty(event, "persisted", { value: true });
  f.w.dispatchEvent(event);
  assert.equal(f.$("workspace").hidden, true);
});
test("initial setup requires QR enrollment before code confirmation", async (t) => {
  const f = fixture(t, { setup: true });
  await settle();
  assert.equal(f.$("code-label").hidden, true);
  f.$("account").value = "新管理员";
  f.$("auth-form").dispatchEvent(new f.w.Event("submit", { cancelable: true }));
  await settle();
  assert.equal(f.$("enroll-box").hidden, false);
  assert.equal(f.$("account").readOnly, true);
  assert.equal(f.$("secret-copy").textContent, "TESTONLYSECRET");
  f.$("code").value = "123456";
  f.$("auth-form").dispatchEvent(new f.w.Event("submit", { cancelable: true }));
  await settle();
  assert.equal(f.$("workspace").hidden, false);
  assert.equal(f.$("secret-copy").textContent, "");
  assert.equal(
    f.calls.filter((c) => c.path === "/api/enroll/confirm").length,
    1,
  );
});
test("messages render untrusted text literally, not as markup", async (t) => {
  const f = fixture(t, {
    messages: [
      {
        id: "1",
        device: "我的设备",
        text: "<img src=x onerror=alert(1)> 🌿",
        created: Date.now(),
        expires: 0,
        file_id: "",
      },
    ],
  });
  await settle();
  await login(f);
  assert.match(f.$("timeline").textContent, /<img src=x onerror=alert\(1\)>/);
  assert.equal(f.$("timeline").querySelectorAll("img").length, 0);
  assert.equal(f.$("message-count").textContent, "1");
});
test("text sender includes device, TTL and restores editable controls", async (t) => {
  const f = fixture(t);
  await settle();
  await login(f);
  f.$("message-text").value = "来自电脑的消息";
  f.$("retention").value = "3600";
  f.$("send").click();
  await settle();
  const call = f.calls.find(
    (c) => c.path === "/api/messages" && c.opts.method === "POST",
  );
  assert.ok(call);
  assert.deepEqual(JSON.parse(call.opts.body), {
    text: "来自电脑的消息",
    device: "我的电脑",
    ttl: 3600,
  });
  assert.equal(
    call.opts.headers.get("Authorization"),
    "Bearer test-only-bearer",
  );
  assert.equal(f.$("message-text").value, "");
  assert.equal(f.$("message-text").disabled, false);
  assert.equal(f.$("send").disabled, false);
});
test("failed upload retains the pending file and message for retry", async (t) => {
  const f = fixture(t);
  await settle();
  await login(f);
  f.w.XMLHttpRequest = class {
    constructor() {
      this.upload = {};
      this.status = 413;
      this.responseText = JSON.stringify({ error: "超出容量" });
    }
    open() {}
    setRequestHeader() {}
    send() {
      queueMicrotask(() => this.onload());
    }
  };
  f.w.__test.state.files.push(new f.w.File(["sample"], "example.txt"));
  f.w.__test.renderAttachments();
  f.$("message-text").value = "保留这段文字";
  f.$("send").click();
  await settle();
  assert.equal(f.w.__test.state.files.length, 1);
  assert.equal(f.$("message-text").value, "保留这段文字");
  assert.equal(f.$("send").disabled, false);
  assert.match(f.$("toast").textContent, /超出容量/);
});
test("admin settings are editable without sending a message first", async (t) => {
  const f = fixture(t);
  await settle();
  await login(f);
  await f.w.__test.setView("admin");
  assert.equal(f.$("admin-view").hidden, false);
  assert.equal(f.$("active-store").value, "local");
  f.$("guest-hours").value = "6";
  f.$("public-registration").checked = true;
  f.$("admin-form").dispatchEvent(
    new f.w.SubmitEvent("submit", {
      cancelable: true,
      submitter: f.$("admin-form").querySelector("button"),
    }),
  );
  await settle();
  const call = f.calls.find(
    (c) => c.path === "/api/admin" && c.opts.method === "PUT",
  );
  assert.ok(call);
  assert.equal(JSON.parse(call.opts.body).guest_hours, 6);
  assert.equal(JSON.parse(call.opts.body).public_registration, true);
});
test("static UI contracts include viewport, narrow-screen layout and no external dependencies", () => {
  assert.match(html, /name="viewport"/);
  const css = fs.readFileSync("web/style.css", "utf8");
  assert.match(css, /@media\(max-width:700px\)/);
  assert.doesNotMatch(html, /<script[^>]+src="https?:/);
  assert.doesNotMatch(css, /@import/);
  for (const match of script.matchAll(/\$\(["']([^"']+)["']\)/g))
    assert.ok(
      html.includes('id="' + match[1] + '"'),
      "missing element: " + match[1],
    );
});

test("Telegram controls default off, identify chat and independently save directions", async (t) => {
  const f = fixture(t);
  await settle();
  await login(f);
  await f.w.__test.setView("admin");
  assert.equal(f.$("tg-enabled").checked, false);
  assert.equal(f.$("tg-private").checked, false);
  f.$("tg-token").value = "test-only-token";
  f.$("tg-test").click();
  await settle();
  assert.equal(f.$("tg-chat").value, "42");
  assert.equal(f.$("tg-enabled").checked, false);
  f.$("tg-enabled").checked = true;
  f.$("tg-send").checked = false;
  f.$("telegram-form").dispatchEvent(
    new f.w.SubmitEvent("submit", {
      cancelable: true,
      submitter: f.$("telegram-form").querySelector("button.primary"),
    }),
  );
  await settle();
  const call = f.calls.find(
    (c) => c.path === "/api/admin/telegram" && c.opts.method === "PUT",
  );
  assert.ok(call);
  const body = JSON.parse(call.opts.body);
  assert.equal(body.enabled, true);
  assert.equal(body.receive, true);
  assert.equal(body.send, false);
  assert.equal(body.include_concealed, false);
  assert.equal(body.user_id, "owner");
  assert.equal(body.chat_id, "42");
  assert.equal(body.revision, "test-revision");
  assert.equal(f.$("tg-token").value, "");
  assert.equal(f.w.localStorage.getItem("pr-token"), null);
});

test("history is folded at login and search defaults to all records with custom dates", async (t) => {
  const f = fixture(t);
  await settle();
  await login(f);
  assert.ok(f.calls.some((c) => c.path === "/api/messages?scope=current"));
  assert.equal(f.$("open-search").hidden, true);
  assert.equal(f.$("search-dialog").open, false);
  await f.w.__test.setView("all");
  assert.equal(f.$("open-search").hidden, false);
  assert.equal(f.$("search-dialog").open, false);
  f.$("open-search").click();
  assert.equal(f.$("search-dialog").open, true);
  assert.equal(f.w.document.activeElement, f.$("search-text"));
  assert.ok(f.calls.some((c) => c.path === "/api/messages?scope=all"));
  await f.w.__test.setView("files");
  assert.equal(f.$("open-search").hidden, true);
  assert.equal(f.$("search-dialog").open, false);
  await f.w.__test.setView("all");
  f.$("search-text").value = "测试 %_";
  f.$("search-period").value = "custom";
  f.$("search-period").dispatchEvent(new f.w.Event("change"));
  f.$("search-from").value = "2026-01-01T00:00";
  f.$("search-to").value = "2026-02-01T00:00";
  f.$("search-form").dispatchEvent(
    new f.w.Event("submit", { cancelable: true }),
  );
  await settle();
  const last = f.calls
    .filter((c) => c.path.startsWith("/api/messages?"))
    .at(-1);
  const query = new URL(last.path, "http://localhost").searchParams;
  assert.equal(query.get("scope"), "all");
  assert.equal(query.has("files"), false);
  assert.equal(f.w.__test.state.view, "all");
  assert.equal(query.get("q"), "测试 %_");
  assert.equal(f.$("search-dialog").open, false);
  assert.equal(f.w.document.activeElement, f.$("open-search"));
  assert.ok(Number(query.get("from")) < Number(query.get("to")));
  f.$("clear-search").click();
  await settle();
  assert.equal(f.w.__test.state.filters, "");
  assert.equal(f.w.__test.state.view, "all");
});

test("concealed text loads only on reveal and is concealed again after a page reload", async (t) => {
  const messages = [
    {
      id: "secret",
      device: "test",
      text: "",
      concealed: true,
      created: Date.now(),
      expires: 0,
      file_id: "",
    },
  ];
  const f = fixture(t, { messages });
  await settle();
  await login(f);
  assert.match(f.$("timeline").textContent, /点击查看完整消息/);
  assert.equal(
    f.calls.some((c) => c.path.endsWith("/text")),
    false,
  );
  [...f.$("timeline").querySelectorAll("button")]
    .find((b) => b.getAttribute("aria-label") === "查看隐私消息")
    .click();
  await settle();
  assert.match(f.$("timeline").textContent, /revealed test body/);
  assert.equal(
    f.w.sessionStorage.getItem("pr-session").includes("revealed test body"),
    false,
  );
  const fresh = fixture(t, {
    messages,
    navigation: "reload",
    sessionToken: "test-only-bearer",
  });
  await settle();
  assert.doesNotMatch(fresh.$("timeline").textContent, /revealed test body/);
  assert.match(fresh.$("timeline").textContent, /点击查看完整消息/);
  f.$("concealed-send").checked = true;
  f.$("message-text").value = "masked";
  f.$("send").click();
  await settle();
  assert.equal(
    JSON.parse(
      f.calls.find(
        (c) => c.opts.method === "POST" && c.path === "/api/messages",
      ).opts.body,
    ).concealed,
    true,
  );
});

test("disabled automatic images do not request tickets until an explicit load", async (t) => {
  const f = fixture(t, {
    autoLoadImages: false,
    messages: [
      {
        id: "photo",
        device: "test",
        text: "",
        created: Date.now(),
        expires: 0,
        file_id: "file",
        name: "sample.png",
        mime: "image/png",
        size: 10,
      },
    ],
  });
  await settle();
  await login(f);
  assert.equal(f.$("timeline").querySelectorAll("img").length, 0);
  assert.equal(
    f.calls.some((c) => c.path.includes("/ticket")),
    false,
  );
  [...f.$("timeline").querySelectorAll("button")]
    .find((b) => b.getAttribute("aria-label") === "加载图片")
    .click();
  await settle();
  assert.equal(f.$("timeline").querySelectorAll("img").length, 1);
  assert.equal(
    f.calls.some((c) => c.path.includes("/ticket")),
    true,
  );
});

test("links are semantic safe anchors and concealed links only appear after tapping the message", async (t) => {
  const text =
    "https://example.com alice@example.com 13800138000 <img src=x onerror=alert(1)> javascript:alert(1)";
  const f = fixture(t, {
    messages: [
      {
        id: "link-private",
        device: "test",
        text: "",
        concealed: true,
        created: Date.now(),
        expires: 0,
        file_id: "",
      },
    ],
    revealText: text,
  });
  await settle();
  await login(f);
  assert.equal(f.$("timeline").querySelectorAll("a").length, 0);
  f.$("timeline").querySelector('[aria-label="查看隐私消息"]').click();
  await settle();
  const links = f.$("timeline").querySelectorAll(".message-link");
  assert.equal(links.length, 3);
  assert.equal(links[0].target, "_blank");
  assert.match(links[0].rel, /noopener/);
  assert.match(links[0].rel, /noreferrer/);
  assert.equal(links[1].getAttribute("href"), "mailto:alice@example.com");
  assert.equal(links[2].getAttribute("href"), "tel:13800138000");
  assert.equal(f.$("timeline").querySelector("img"), null);
  assert.equal(
    f.$("timeline").querySelector(".message-text").textContent,
    text,
  );
  [...f.$("timeline").querySelectorAll("button")]
    .find((b) => b.textContent === "收起")
    .click();
  assert.equal(f.$("timeline").querySelectorAll("a").length, 0);
});

test("private image placeholder requests no ticket until reveal; thumbnail opens an original and closes cleanly", async (t) => {
  const f = fixture(t, {
    messages: [
      {
        id: "private-picture",
        device: "test",
        text: "",
        concealed: true,
        created: Date.now(),
        expires: 0,
        file_id: "picture",
        name: "photo.png",
        mime: "image/png",
        size: 10,
      },
    ],
    revealText: "",
  });
  await settle();
  await login(f);
  assert.equal(f.$("timeline").querySelector("img"), null);
  assert.equal(
    f.calls.some((c) => c.path.includes("/ticket")),
    false,
  );
  assert.match(f.$("timeline").textContent, /photo.png/);
  f.$("timeline").querySelector('[aria-label="查看隐私消息"]').click();
  await settle();
  assert.ok(f.$("timeline").querySelector(".message-image"));
  f.$("timeline").querySelector(".image-thumb").click();
  await settle();
  assert.equal(f.$("image-viewer").hidden, false);
  assert.match(f.$("image-viewer-image").getAttribute("src"), /inline=1/);
  f.$("image-viewer-close").click();
  assert.equal(f.$("image-viewer").hidden, true);
  assert.equal(f.$("image-viewer-image").getAttribute("src"), null);
});

test("privacy can be changed after sending and shared font size applies without storing message contents", async (t) => {
  const f = fixture(t, {
    messageSize: "small",
    messages: [
      {
        id: "toggle",
        device: "test",
        text: "normal@example.com",
        concealed: false,
        created: Date.now(),
        expires: 0,
        file_id: "",
      },
    ],
  });
  await settle();
  await login(f);
  assert.equal(f.w.document.documentElement.dataset.messageSize, "small");
  assert.equal(f.$("timeline").querySelectorAll("a").length, 1);
  [...f.$("timeline").querySelectorAll("button")]
    .find((b) => b.textContent === "设为隐私")
    .click();
  await settle();
  assert.equal(f.$("timeline").querySelectorAll("a").length, 0);
  assert.ok(f.$("timeline").querySelector('[aria-label="查看隐私消息"]'));
  const put = f.calls.find((c) => c.path.endsWith("/privacy"));
  assert.deepEqual(JSON.parse(put.opts.body), { concealed: true });
  [...f.$("timeline").querySelectorAll("button")]
    .find((b) => b.textContent === "取消隐私")
    .click();
  await settle();
  assert.equal(
    f.$("timeline").querySelector('[aria-label="查看隐私消息"]'),
    null,
  );
  await f.w.__test.setView("admin");
  f.$("message-size").value = "large";
  f.$("admin-form").dispatchEvent(
    new f.w.SubmitEvent("submit", {
      cancelable: true,
      submitter: f.$("admin-form").querySelector("button.primary"),
    }),
  );
  await settle();
  const save = f.calls.findLast(
    (c) => c.path === "/api/admin" && c.opts.method === "PUT",
  );
  assert.equal(JSON.parse(save.opts.body).message_size, "large");
  assert.equal(f.w.document.documentElement.dataset.messageSize, "large");
  assert.equal(f.$("emoji"), null);
  assert.equal(f.$("message-text").rows, 1);
});

test("video remains preload none and never autoplays", async (t) => {
  const f = fixture(t, {
    messages: [
      {
        id: "video",
        device: "test",
        text: "",
        created: Date.now(),
        expires: 0,
        file_id: "video",
        name: "video.mp4",
        mime: "video/mp4",
        size: 10,
      },
    ],
  });
  await settle();
  await login(f);
  const video = f.$("timeline").querySelector("video");
  assert.equal(video.preload, "none");
  assert.equal(video.autoplay, false);
  assert.equal(video.controls, true);
});

test("late reveal response cannot expose content after privacy revision changes", async (t) => {
  const f = fixture(t, {
    messages: [
      {
        id: "late",
        device: "test",
        text: "",
        concealed: true,
        privacy_version: 0,
        created: Date.now(),
        expires: 0,
        file_id: "",
      },
    ],
  });
  await settle();
  await login(f);
  const fetch = f.w.fetch;
  let release;
  f.w.fetch = (path, opts) =>
    path.endsWith("/text")
      ? new Promise((resolve) => {
          release = () =>
            resolve(
              new Response(
                JSON.stringify({
                  text: "late-secret@example.com",
                  privacy_version: 0,
                }),
                { status: 200 },
              ),
            );
        })
      : fetch(path, opts);
  f.$("timeline").querySelector('[aria-label="查看隐私消息"]').click();
  await settle();
  f.w.__test.state.privacyRevision++;
  f.w.__test.state.messages[0].privacy_version = 1;
  release();
  await settle();
  assert.equal(f.w.__test.state.revealed.has("late"), false);
  assert.equal(f.$("timeline").querySelectorAll("a").length, 0);
});

test("late thumbnail and original requests cannot load or reopen after context changes", async (t) => {
  const f = fixture(t, {
    autoLoadImages: false,
    messages: [
      {
        id: "late-image",
        device: "test",
        text: "",
        created: Date.now(),
        expires: 0,
        file_id: "late-file",
        name: "late.png",
        mime: "image/png",
        size: 10,
      },
    ],
  });
  await settle();
  await login(f);
  const fetch = f.w.fetch;
  let release;
  f.w.fetch = (path, opts) =>
    path.includes("/ticket")
      ? new Promise((resolve) => {
          release = () =>
            resolve(
              new Response(JSON.stringify({ url: "/file/late?ticket=test" }), {
                status: 200,
              }),
            );
        })
      : fetch(path, opts);
  f.$("timeline").querySelector('[aria-label="加载图片"]').click();
  await settle();
  const thumbnail = f.$("timeline").querySelector("img");
  f.w.__test.state.generation++;
  release();
  await settle();
  assert.equal(thumbnail.getAttribute("src"), null);
  f.$("timeline").querySelector(".image-thumb").click();
  await settle();
  f.w.__test.state.privacyRevision++;
  release();
  await settle();
  assert.equal(f.$("image-viewer").hidden, true);
  assert.equal(f.$("image-viewer-image").getAttribute("src"), null);
});

test("language switch preserves draft, session and user content without business requests", async t => {
 const f=fixture(t,{language:true});await settle();await login(f);
 f.$("message-text").value="保存设置 <script>never execute</script>";
 const userContent=f.w.document.createElement("p");userContent.className="message-text";userContent.textContent="本次消息 设为隐私";f.$("timeline").append(userContent);
 const before=f.calls.length,session=f.w.__test.browserSession;
 f.w.AppI18n.set("en");await settle();
 assert.equal(f.$("message-text").value,"保存设置 <script>never execute</script>");
 assert.equal(userContent.textContent,"本次消息 设为隐私");
 assert.equal(f.w.__test.browserSession,session);
 assert.equal(f.calls.length,before);
 assert.match(f.w.document.querySelector('[data-view="all"]').textContent,/All messages/);
 f.w.AppI18n.set("zh");assert.equal(userContent.textContent,"本次消息 设为隐私");
});

test("drawer waits for paint before focusing and returns focus on close",async t=>{
 const f=fixture(t);await settle();await login(f);
 const frames=[];f.w.requestAnimationFrame=fn=>{frames.push(fn);return frames.length};
 f.w.eval(fs.readFileSync("web/appearance.js","utf8"));
 const trigger=f.$("mobile-menu"),first=f.w.document.querySelector('#relay-nav a'),close=f.w.document.querySelector('.drawer-backdrop');
 assert.equal(f.w.document.querySelector('#relay-nav [data-drawer-close]'),null);
 for(const el of f.w.document.querySelectorAll('#relay-nav button,#relay-nav a'))el.getClientRects=()=>[{width:44,height:44}];
 trigger.click();assert.notEqual(f.w.document.activeElement,first);
 while(frames.length)frames.shift()();
 assert.equal(f.w.document.activeElement,first);
 close.click();assert.equal(f.w.document.activeElement,trigger);
 assert.equal(f.$("workspace").classList.contains("drawer-open"),false);
});

test("search dialog closes on navigation, clear, logout and button without losing composer draft", async t => {
 const f=fixture(t);await settle();await login(f);
 assert.equal(f.w.document.querySelector(".inbox-intro-compact"), null);
 assert.equal(f.w.document.querySelector(".inbox-intro p"),null);
 await f.w.__test.setView("all");
 f.$("message-text").value="draft";
 f.$("open-search").click();f.$("close-search").click();
 assert.equal(f.$("search-dialog").open,false);
 assert.equal(f.w.document.activeElement,f.$("open-search"));
 f.$("open-search").click();await f.w.__test.setView("files");
 assert.equal(f.$("search-dialog").open,false);
 assert.equal(f.$("open-search").hidden,true);
 assert.equal(f.$("message-text").value,"draft");
 await f.w.__test.setView("all");f.$("open-search").click();
 f.$("clear-search").click();await settle();
 assert.equal(f.$("search-dialog").open,false);
 f.$("open-search").click();f.$("logout").click();await settle();
 assert.equal(f.$("search-dialog").open,false);
});

test("workspace theme and language are compact topbar controls; message actions stay transparent", async t => {
 const f=fixture(t);await settle();
 assert.equal(f.w.document.querySelector(".topbar [data-appearance-open]"),null);
 assert.ok(f.w.document.querySelector(".topbar [data-lang-toggle]"));
 assert.ok(f.w.document.querySelector(".topbar [data-theme-choice='system']"));
 assert.equal(f.w.document.querySelector("#relay-nav [data-appearance-open]"), null);
 assert.equal(f.w.document.querySelector("#relay-nav [data-lang-toggle]"), null);
 const css=fs.readFileSync("web/design-system.css","utf8");
 assert.match(css,/\.message-actions a\{background:transparent/);
 assert.match(css,/\.composer textarea\{min-height:64px/);
});

test("remembered startup keeps login hidden until validated and network errors do not masquerade as logout", async t => {
 let release;const gate=new Promise(r=>release=r);
 const f=fixture(t,{remembered:true,restoreWait:gate});await settle();
 assert.equal(f.$("auth").hidden,true);
 assert.equal(f.$("workspace").hidden,true);
 assert.equal(f.$("session-loading").hidden,false);
 release();await settle();
 assert.equal(f.$("auth").hidden,true);
 assert.equal(f.$("workspace").hidden,false);
 assert.equal(f.$("session-loading").hidden,true);
 const failed=fixture(t,{remembered:true,restoreStatus:503});await settle();
 assert.equal(failed.$("auth").hidden,true);
 assert.equal(failed.$("session-retry").hidden,false);
});

test("current-session badge is independent of all/file lists and search",async t=>{
 const messages=Array.from({length:5},(_,i)=>({id:"m"+i,text:"history",device:"test",created:Date.now()+i,expires:0,file_id:""}));
 const f=fixture(t,{messages,currentCount:2});await settle();await login(f);
 assert.equal(f.$("message-count").textContent,"2");
 for(const view of ["all","files","messages"]) {
   await f.w.__test.setView(view);
   assert.equal(f.$("message-count").textContent,"2");
 }
 assert.equal(f.w.document.querySelector("nav [data-view]").dataset.view,"all");
 assert.equal(f.w.__test.state.view,"messages");
 assert.equal(f.w.document.querySelector('[data-view="files"]').textContent.trim(),"文件信息");
});

test("home is in-app navigation and repeated menu selection does not refetch or reset draft",async t=>{
 const f=fixture(t,{remembered:true});await settle();
 f.$("message-text").value="keep draft";
 await f.w.__test.setView("all");
 const anchor=f.w.document.querySelector("#relay-nav .brand");
 const event=new f.w.MouseEvent("click",{bubbles:true,cancelable:true});
 anchor.dispatchEvent(event);await settle();
 assert.equal(event.defaultPrevented,true);
 assert.equal(f.w.__test.state.view,"messages");
 assert.equal(f.$("message-text").value,"keep draft");
 const count=f.calls.length;await f.w.__test.setView("messages");
 assert.equal(f.calls.length,count);
 assert.equal(f.$("auth").hidden,true);
});

test("rapid navigation aborts previous message requests and ignores their late content/count",async t=>{
 const f=fixture(t,{currentCount:1});await settle();await login(f);
 const original=f.w.fetch;let release,signal;
 f.w.fetch=async(path,opts)=>path==="/api/messages?scope=all"?await new Promise(r=>{release=r;signal=opts.signal}):original(path,opts);
 const old=f.w.__test.setView("all");await settle();
 await f.w.__test.setView("files");
 assert.equal(signal.aborted,true);
 release(new Response(JSON.stringify({messages:[{id:"late",text:"stale",device:"test",created:Date.now()}],current_count:999})));
 await old;await settle();
 assert.equal(f.w.__test.state.view,"files");
 assert.equal(f.$("message-count").textContent,"1");
 assert.doesNotMatch(f.$("timeline").textContent,/stale/);
});

test("polling preserves unchanged message rows and media rather than rebuilding everything",async t=>{
 const messages=[{id:"first",text:"one",device:"test",created:Date.now(),expires:0,file_id:""}];
 const f=fixture(t,{messages});await settle();await login(f);
 const first=f.$("timeline").querySelector(".message");
 messages.push({id:"second",text:"two",device:"test",created:Date.now()+1,expires:0,file_id:""});
 await f.w.__test.refresh();
 assert.equal(f.$("timeline").querySelector(".message"),first);
 assert.equal(f.$("timeline").querySelectorAll(".message").length,2);
});
