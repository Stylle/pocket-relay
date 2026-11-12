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
  } = {},
) {
  const dom = new JSDOM(html, {
    url: "http://localhost/",
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  t.after(() => dom.window.close());
  const w = dom.window;
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
      if (!remembered) return new Response(JSON.stringify({error:"not remembered"}), {status:401});
      value = {token:"remembered-memory-token",user,remembered:true,idle_minutes:0,expires_at:Date.now()+7*86400000,server_time:Date.now(),last_active:Date.now()};
    } else if (path === "/api/status")
      value = {
        setup,
        public_registration: publicRegistration,
        guest_hours: 1,
      };
    else if (path === "/api/login" || path === "/api/enroll/confirm")
      value = { token: "test-only-bearer", user, remembered: JSON.parse(opts.body).remember === true, idle_minutes: JSON.parse(opts.body).remember ? 0 : 15 };
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
      value = { text: "revealed test body" };
    else if (path.startsWith("/api/messages"))
      value =
        opts.method === "POST"
          ? { id: "new-message" }
          : {
              messages,
              user,
              device_template: "{device} · {ip}",
              max_file_mb: 256,
              auto_load_images: autoLoadImages,
            };
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
  w.eval(fs.readFileSync("web/session.js", "utf8"));
  w.eval(
    script +
      "\nwindow.__test={state,setView,renderAttachments,browserSession,recordActivity,flushActivity,expireSession};",
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
  const call = ordinary.calls.find(c => c.path === "/api/login");
  assert.equal(JSON.parse(call.opts.body).remember, true);
  assert.equal(ordinary.w.__test.browserSession.remembered, true);
  const restored = fixture(t, { navigation: "navigate", remembered: true });
  await settle();
  assert.equal(restored.$("workspace").hidden, false);
  assert.equal(restored.w.__test.browserSession.remembered, true);
  const restoreCall = restored.calls.find(c => c.path === "/api/session/restore");
  assert.equal(restoreCall.opts.method, "POST");
  assert.equal(restoreCall.opts.headers.get("X-Relay-Session"), "1");
  assert.equal(restored.calls.some(c => c.path === "/api/login"), false);
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
  assert.equal(f.$("search-form").hidden, true);
  await f.w.__test.setView("all");
  assert.equal(f.$("search-form").hidden, false);
  assert.ok(f.calls.some((c) => c.path === "/api/messages?scope=all"));
  await f.w.__test.setView("files");
  assert.equal(f.$("search-form").hidden, true);
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
  assert.match(f.$("timeline").textContent, /正文已遮挡/);
  assert.equal(
    f.calls.some((c) => c.path.endsWith("/text")),
    false,
  );
  [...f.$("timeline").querySelectorAll("button")]
    .find((b) => b.textContent === "显示正文")
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
  assert.match(fresh.$("timeline").textContent, /正文已遮挡/);
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
    .find((b) => b.textContent === "加载图片")
    .click();
  await settle();
  assert.equal(f.$("timeline").querySelectorAll("img").length, 1);
  assert.equal(
    f.calls.some((c) => c.path.includes("/ticket")),
    true,
  );
});
