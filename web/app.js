"use strict";
const $ = (id) => document.getElementById(id);
const state = {
  token: "",
  user: null,
  mode: "login",
  enrollment: "",
  status: null,
  files: [],
  messages: [],
  view: "messages",
  busy: false,
  polling: false,
  signature: "",
  admin: null,
  older: [],
  maxMB: 256,
  filters: "",
  generation: 0,
  revealed: new Map(),
  loadedImages: new Set(),
  autoLoadImages: true,
  hasMore: false,
};
let toastTimer;
let navigationType = "navigate";
try {
  navigationType =
    performance.getEntriesByType("navigation")[0]?.type ||
    (performance.navigation?.type === 1 ? "reload" : "navigate");
} catch {}
let sessionStorageSafe;
try {
  sessionStorageSafe = window.sessionStorage;
} catch {
  sessionStorageSafe = {
    getItem() {
      return null;
    },
    setItem() {
      throw Error("storage unavailable");
    },
    removeItem() {},
  };
}
const browserSession = new RelaySession(sessionStorageSafe, navigationType);
let activityPending = false,
  activityBusy = false,
  activityTimer = 0;

function toast(text, error = false) {
  $("toast").textContent = text;
  $("toast").className = error ? "error-toast" : "";
  $("toast").hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => ($("toast").hidden = true), 5000);
}
function node(tag, cls, text) {
  const n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}
function button(text, action, cls = "") {
  const b = node("button", cls, text);
  b.type = "button";
  b.addEventListener("click", () =>
    Promise.resolve(action()).catch((e) => toast(e.message, true)),
  );
  return b;
}
function saved(key, fallback = "") {
  try {
    return localStorage.getItem(key) || fallback;
  } catch {
    return fallback;
  }
}
function save(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {}
}
function device() {
  return saved(
    "pr-device",
    /Android|iPhone|iPad/i.test(navigator.userAgent) ? "我的手机" : "我的电脑",
  );
}
function size(n) {
  if (n < 1024) return n + " B";
  if (n < 1024 ** 2) return (n / 1024).toFixed(1) + " KB";
  if (n < 1024 ** 3) return (n / 1024 ** 2).toFixed(1) + " MB";
  return (n / 1024 ** 3).toFixed(2) + " GB";
}
function date(n) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(n));
}
async function api(path, options = {}) {
  const requestToken = state.token;
  const headers = new Headers(options.headers);
  if (state.token) headers.set("Authorization", "Bearer " + state.token);
  if (
    options.body &&
    typeof options.body === "string" &&
    !headers.has("Content-Type")
  )
    headers.set("Content-Type", "application/json");
  const res = await fetch(path, { ...options, headers, cache: "no-store" });
  if (requestToken && requestToken !== state.token)
    throw Error("登录状态已改变，请重新操作");
  if (!res.ok && res.status !== 304) {
    const b = await res.json().catch(() => ({}));
    if (res.status === 401 && state.token) {
      logoutLocal();
      toast(b.error || "请重新验证", true);
    }
    throw Error(b.error || "请求失败，请检查网络（" + res.status + "）");
  }
  return res;
}
async function json(path, options = {}) {
  return (await api(path, options)).json();
}
async function action(b, fn) {
  b.disabled = true;
  try {
    return await fn();
  } catch (e) {
    toast(e.message, true);
  } finally {
    b.disabled = false;
  }
}
function authMode(mode) {
  state.mode = mode;
  state.enrollment = "";
  $("enroll-box").hidden = true;
  $("qr").removeAttribute("src");
  $("secret-copy").textContent = "";
  $("account").readOnly = false;
  $("code").value = "";
  $("auth-error").textContent = "";
  const register = mode === "register";
  $("auth-title").textContent = register
    ? state.status.setup
      ? "创建你的私人空间"
      : "创建临时空间"
    : "欢迎回来";
  $("auth-description").textContent = register
    ? "先添加验证器，再用一组验证码确认绑定。"
    : "打开验证器，输入此刻的六位验证码。";
  $("code-label").hidden = register;
  $("code").required = !register;
  $("auth-submit").textContent = register
    ? "生成验证器二维码 →"
    : "进入我的空间 →";
  $("auth-switch").hidden =
    state.status.setup || !state.status.public_registration;
  $("auth-switch").textContent = register
    ? "已有账户？返回登录"
    : "还没有账户？创建临时空间";
  $("guest-notice").textContent =
    register && !state.status.setup
      ? "此账户自完成注册起有效 " +
        state.status.guest_hours +
        " 小时，每个账户的数据独立。"
      : "";
}
async function enterWorkspace(result) {
  state.token = result.token;
  browserSession.accept(result.token, result);
  state.user = result.user;
  save("pr-account", result.user.name);
  $("code").value = "";
  $("secret-copy").textContent = "";
  $("qr").removeAttribute("src");
  state.enrollment = "";
  $("auth").hidden = true;
  $("workspace").hidden = false;
  $("account-name").textContent = result.user.name;
  $("account-avatar").textContent = result.user.name.slice(0, 1);
  $("account-role").textContent = result.user.admin
    ? "永久管理员"
    : "有效至 " + date(result.user.expires * 1000);
  $("admin-nav").hidden = !result.user.admin;
  $("device-name").value = device();
  $("sending-device").textContent = "来自 " + device();
  await setView("messages");
}
async function boot() {
  try {
    state.status = await json("/api/status");
    $("account").value = saved("pr-account");
    const token = browserSession.restore();
    if (token) {
      state.token = token;
      try {
        const result = await json("/api/session");
        await enterWorkspace({ ...result, token });
        return;
      } catch (e) {
        if (state.token) {
          $("auth-error").textContent = "连接服务失败，请刷新重试。";
          return;
        }
      }
    }
    authMode(state.status.setup ? "register" : "login");
  } catch (e) {
    $("auth-error").textContent = "连接服务失败，请刷新重试。";
  }
}
$("auth-switch").onclick = () =>
  authMode(state.mode === "login" ? "register" : "login");
$("secret-copy").onclick = () => copyText($("secret-copy").textContent);
$("auth-form").onsubmit = async (e) => {
  e.preventDefault();
  $("auth-error").textContent = "";
  const b = $("auth-submit");
  b.disabled = true;
  try {
    const name = $("account").value.trim();
    if (state.mode === "register" && !state.enrollment) {
      const result = await json("/api/enroll", {
        method: "POST",
        body: JSON.stringify({ name }),
      });
      state.enrollment = result.enrollment;
      $("qr").src = result.qr;
      $("secret-copy").textContent = result.secret;
      $("enroll-box").hidden = false;
      $("code-label").hidden = false;
      $("code").required = true;
      $("account").readOnly = true;
      $("auth-submit").textContent = "确认绑定并进入 →";
      $("code").focus();
      return;
    }
    const result = await json(
      state.enrollment ? "/api/enroll/confirm" : "/api/login",
      {
        method: "POST",
        body: JSON.stringify(
          state.enrollment
            ? { enrollment: state.enrollment, code: $("code").value }
            : { name, code: $("code").value },
        ),
      },
    );
    await enterWorkspace(result);
  } catch (err) {
    $("auth-error").textContent = err.message;
  } finally {
    b.disabled = false;
  }
};
function logoutLocal() {
  state.generation++;
  state.revealed.clear();
  state.loadedImages.clear();
  state.filters = "";
  $("search-form").reset();
  browserSession.clear();
  activityPending = false;
  clearTimeout(activityTimer);
  state.admin = null;
  state.token = "";
  state.user = null;
  state.files = [];
  state.messages = [];
  state.older = [];
  state.signature = "";
  $("workspace").hidden = true;
  $("auth").hidden = false;
  $("timeline").replaceChildren();
  $("message-text").value = "";
  renderAttachments();
  authMode("login");
}
$("logout").onclick = async () => {
  try {
    await json("/api/logout", { method: "POST" });
  } finally {
    logoutLocal();
  }
};
document
  .querySelectorAll("[data-view]")
  .forEach(
    (b) =>
      (b.onclick = () =>
        setView(b.dataset.view).catch((e) => toast(e.message, true))),
  );
$("mobile-menu").onclick = () => $("workspace").classList.toggle("menu-open");
async function setView(view) {
  state.generation++;
  state.polling = false;
  state.older = [];
  state.hasMore = false;
  state.messages = [];
  state.filters = "";
  $("search-form").reset();
  $("search-custom").hidden = true;
  $("search-from").required = false;
  $("search-to").required = false;
  $("search-status").textContent =
    view === "messages"
      ? "历史已折叠；搜索默认覆盖全部记录。"
      : "搜索默认覆盖全部文字记录。";
  state.view = view;
  $("workspace").classList.remove("menu-open");
  document
    .querySelectorAll("[data-view]")
    .forEach((b) => b.classList.toggle("active", b.dataset.view === view));
  $("view-title").textContent = {
    messages: "本次消息 ✦",
    all: "全部信息",
    files: "文件与影像",
    settings: "设备设置",
    admin: "管理后台",
  }[view];
  $("messages-view").hidden = !["messages", "all", "files"].includes(view);
  $("settings-view").hidden = view !== "settings";
  $("admin-view").hidden = view !== "admin";
  $("composer").hidden = !["messages", "all", "files"].includes(view);
  if (view === "admin") await loadAdmin();
  else if (["messages", "all", "files"].includes(view)) {
    state.signature = "";
    renderMessages();
    await refresh();
  }
}
$("device-form").onsubmit = (e) => {
  e.preventDefault();
  save("pr-device", $("device-name").value.trim());
  $("sending-device").textContent = "来自 " + device();
  toast("设备名称已保存");
};
async function ticket(id) {
  return (await json("/api/files/" + id + "/ticket", { method: "POST" })).url;
}
async function download(m) {
  const url = await ticket(m.file_id);
  const a = node("a");
  a.href = url;
  a.download = m.name;
  a.rel = "noreferrer";
  document.body.append(a);
  a.click();
  a.remove();
}
async function copyText(text) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
  } else {
    const t = node("textarea");
    t.value = text;
    document.body.append(t);
    t.select();
    const ok = document.execCommand("copy");
    t.remove();
    if (!ok) throw Error("当前浏览器不允许复制，请使用 HTTPS 或手动选择文字");
  }
  toast("已复制");
}
async function copyImage(m) {
  if (
    !navigator.clipboard?.write ||
    !window.ClipboardItem ||
    !window.isSecureContext
  )
    throw Error("复制图片需要 HTTPS 和支持的浏览器，请改用下载");
  const task = (async () => {
    const url = await ticket(m.file_id);
    const response = await fetch(url);
    if (!response.ok) throw Error("图片读取失败");
    const blob = await response.blob();
    if (blob.type === "image/png") return blob;
    const bitmap = await createImageBitmap(blob);
    try {
      const canvas = document.createElement("canvas");
      canvas.width = bitmap.width;
      canvas.height = bitmap.height;
      if (canvas.width * canvas.height > 32000000)
        throw Error("图片过大，请使用下载");
      canvas.getContext("2d").drawImage(bitmap, 0, 0);
      return await new Promise((resolve, reject) =>
        canvas.toBlob(
          (b) => (b ? resolve(b) : reject(Error("图片转换失败"))),
          "image/png",
        ),
      );
    } finally {
      bitmap.close();
    }
  })();
  await navigator.clipboard.write([new ClipboardItem({ "image/png": task })]);
  toast(
    m.mime === "image/gif" ? "已复制静态首帧，保留动图请下载" : "图片已复制",
  );
}
function isImage(m) {
  return [
    "image/png",
    "image/jpeg",
    "image/gif",
    "image/webp",
    "image/avif",
  ].includes(m.mime);
}
async function loadPreview(element, m, attempt = 0) {
  const retry = () => {
    if (attempt < 4 && state.token) {
      setTimeout(
        () => {
          if (element.isConnected && state.token)
            loadPreview(element, m, attempt + 1);
        },
        500 * 2 ** attempt,
      );
    } else if (element.isConnected)
      element.replaceWith(
        node("p", "muted small", "预览暂不可用或格式不受支持，请尝试下载。"),
      );
  };
  element.onerror = retry;
  try {
    element.src = (await ticket(m.file_id)) + "&inline=1";
  } catch {
    retry();
  }
}
function renderMessages() {
  const timeline = $("timeline");
  const all = [...state.older, ...state.messages]
    .filter(
      (m, i, arr) =>
        arr.findIndex((x) => x.id === m.id) === i &&
        (!m.expires || m.expires > Date.now() / 1000) &&
        (state.view !== "files" || m.file_id),
    )
    .sort((a, b) => a.created - b.created || a.id.localeCompare(b.id));
  const fragment = document.createDocumentFragment();
  if (!all.length) {
    const empty = node("div", "empty");
    empty.append(
      node("span", "", "↗"),
      node(
        "h2",
        "",
        state.view === "files"
          ? "这里，留给值得带走的文件。"
          : "第一条，留给自己。",
      ),
      node("p", "", "粘贴文字、拖入文件，换个设备接着看。"),
    );
    if (state.view === "messages") {
      empty.append(
        node("p", "muted", "历史记录已折叠，本页只显示本次登录后发送的消息。"),
        button("查看全部信息", () => setView("all"), "secondary"),
      );
    } else if (state.filters) {
      empty.replaceChildren(node("h2", "", "没有找到符合条件的消息。"));
    }
    fragment.append(empty);
  }
  for (const m of all) {
    const row = node("article", "message");
    const avatar = node(
      "span",
      "message-avatar",
      /手机|iPhone|Android/i.test(m.device) ? "▯" : "▱",
    );
    const content = node("div", "message-content");
    const meta = node("div", "message-meta");
    meta.append(
      node("strong", "", m.device),
      node("time", "", date(m.created)),
    );
    if (m.expires)
      meta.append(
        node("span", "expiry-badge", date(m.expires * 1000) + " 过期"),
      );
    const bubble = node("div", "bubble");
    if (m.file_id) {
      if (isImage(m)) {
        if (!state.autoLoadImages && !state.loadedImages.has(m.id)) {
          bubble.append(
            node("p", "muted", m.name + " · " + size(m.size)),
            button(
              "加载图片",
              () => {
                state.loadedImages.add(m.id);
                renderMessages();
              },
              "secondary",
            ),
          );
        } else {
          const image = node("img", "message-image");
          image.alt = m.name;
          image.loading = "lazy";
          image.onclick = () =>
            download(m).catch((e) => toast(e.message, true));
          bubble.append(image);
          loadPreview(image, m);
        }
      } else if (m.mime.startsWith("video/") || m.mime.startsWith("audio/")) {
        const media = node(m.mime.startsWith("video/") ? "video" : "audio");
        media.controls = true;
        media.preload = "none";
        bubble.append(media);
        loadPreview(media, m);
      } else {
        const card = node("div", "file-card");
        const info = node("div", "file-info");
        info.append(
          node("strong", "", m.name),
          node(
            "small",
            "",
            size(m.size) +
              " · " +
              (m.name.split(".").pop() || "文件").toUpperCase(),
          ),
        );
        card.append(node("span", "file-icon", "▤"), info);
        bubble.append(card);
      }
    }
    const text = m.concealed ? state.revealed.get(m.id) : m.text;
    if (m.concealed && !state.revealed.has(m.id)) {
      bubble.append(node("p", "concealed-text", "•••••• 正文已遮挡"));
      bubble.append(
        button(
          "显示正文",
          async () => {
            const token = state.token;
            const result = await json("/api/messages/" + m.id + "/text");
            if (token !== state.token) return;
            state.revealed.set(m.id, result.text);
            renderMessages();
          },
          "secondary",
        ),
      );
    } else if (text) bubble.append(node("p", "message-text", text));
    const actions = node("div", "message-actions");
    if (text) actions.append(button("复制文字", () => copyText(text)));
    if (m.concealed && state.revealed.has(m.id))
      actions.append(
        button("隐藏正文", () => {
          state.revealed.delete(m.id);
          renderMessages();
        }),
      );
    if (m.file_id) {
      if (isImage(m)) actions.append(button("复制图片", () => copyImage(m)));
      actions.append(button("下载 · " + size(m.size), () => download(m)));
    }
    actions.append(
      button("删除", async () => {
        if (!confirm("删除这条消息及附件？此操作无法撤销。")) return;
        await json("/api/messages/" + m.id, { method: "DELETE" });
        state.older = state.older.filter((x) => x.id !== m.id);
        state.signature = "";
        await refresh();
        toast("已删除");
      }),
    );
    content.append(meta, bubble, actions);
    row.append(avatar, content);
    fragment.append(row);
  }
  timeline.replaceChildren(fragment);
  $("message-count").textContent = String(
    state.messages.length + state.older.length,
  );
  $("load-older").hidden = !state.hasMore;
}
$("search-period").onchange = () => {
  const custom = $("search-period").value === "custom";
  $("search-custom").hidden = !custom;
  $("search-from").required = custom;
  $("search-to").required = custom;
};
$("clear-search").onclick = () =>
  setView("messages").catch((e) => toast(e.message, true));
$("search-form").onsubmit = async (event) => {
  event.preventDefault();
  const query = new URLSearchParams();
  query.set("q", $("search-text").value.trim());
  const period = $("search-period").value;
  if (period !== "all") {
    const from =
      period === "custom"
        ? new Date($("search-from").value).getTime()
        : period === "today"
          ? new Date().setHours(0, 0, 0, 0)
          : Date.now() - Number(period) * 86400000;
    const to =
      period === "custom"
        ? new Date($("search-to").value).getTime()
        : Date.now() + 1;
    if (!Number.isFinite(from) || !Number.isFinite(to) || from >= to) {
      toast("请选择有效的起止时间", true);
      return;
    }
    query.set("from", from);
    query.set("to", to);
  }
  state.filters = query.toString();
  state.generation++;
  state.polling = false;
  state.older = [];
  state.messages = [];
  state.hasMore = false;
  state.signature = "";
  renderMessages();
  $("view-title").textContent = "搜索结果 · 全部记录";
  $("search-status").textContent =
    "搜索范围：此账户的全部记录（含已折叠历史）。";
  await refresh();
};
function messageQuery() {
  const query = new URLSearchParams(state.filters);
  query.set(
    "scope",
    state.filters || state.view !== "messages" ? "all" : "current",
  );
  if (state.view === "files") query.set("files", "1");
  return query;
}
async function refresh() {
  if (!state.token || state.polling) return;
  if (!["messages", "all", "files"].includes(state.view)) return;
  const generation = state.generation;
  state.polling = true;
  try {
    const response = await api("/api/messages?" + messageQuery(), {
      headers:
        state.signature && state.etag ? { "If-None-Match": state.etag } : {},
    });
    if (generation !== state.generation) return;
    if (response.status === 304) {
      $("sync-state").classList.remove("offline");
      $("sync-state").lastChild.textContent = "已同步";
      return;
    }
    state.etag = response.headers.get("ETag") || "";
    const result = await response.json();
    if (generation !== state.generation) return;
    state.maxMB = result.max_file_mb;
    state.autoLoadImages = result.auto_load_images !== false;
    if (!state.older.length) state.hasMore = !!result.has_more;
    const sig = JSON.stringify([
      result.messages,
      state.autoLoadImages,
      state.hasMore,
    ]);
    if (sig !== state.signature) {
      state.messages = result.messages;
      state.signature = sig;
      renderMessages();
    }
    $("sync-state").classList.remove("offline");
    $("sync-state").lastChild.textContent = "已同步";
  } catch (e) {
    if (generation !== state.generation || !state.token) return;
    $("sync-state").classList.add("offline");
    $("sync-state").lastChild.textContent = "连接中断";
  } finally {
    if (generation === state.generation) state.polling = false;
  }
}
$("load-older").onclick = async () =>
  action($("load-older"), async () => {
    const all = [...state.messages, ...state.older];
    if (!all.length) return;
    const oldest = all.reduce((a, b) =>
      a.created < b.created || (a.created === b.created && a.id < b.id) ? a : b,
    );
    const generation = state.generation;
    const query = messageQuery();
    query.set("before", oldest.created);
    query.set("before_id", oldest.id);
    const result = await json("/api/messages?" + query);
    if (generation !== state.generation) return;
    state.hasMore = !!result.has_more;
    if (!result.messages.length) {
      toast("已经到底啦");
      $("load-older").hidden = true;
      return;
    }
    state.older.push(...result.messages);
    renderMessages();
  });
setInterval(() => {
  if (
    state.token &&
    !document.hidden &&
    ["messages", "all", "files"].includes(state.view)
  )
    refresh();
}, 4000);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden && state.token) refresh();
});
function renderAttachments() {
  const box = $("attachments");
  box.replaceChildren();
  box.hidden = !state.files.length;
  state.files.forEach((f, i) => {
    const tag = node("div", "attachment-tag");
    tag.append(
      node("span", "", f.name + " · " + size(f.size)),
      button("×", () => {
        if (state.busy) return;
        state.files.splice(i, 1);
        renderAttachments();
      }),
    );
    box.append(tag);
  });
}
function addFiles(files) {
  if (state.busy) {
    toast("文件正在发送，请稍后添加", true);
    return;
  }
  for (const f of files) {
    if (f.size > state.maxMB * 1024 ** 2) {
      toast(f.name + " 超过 " + state.maxMB + " MB 限制", true);
      continue;
    }
    if (state.files.length >= 30) {
      toast("每批最多 30 个文件，请分批发送", true);
      break;
    }
    state.files.push(f);
  }
  renderAttachments();
}
$("attach").onclick = () => $("file-input").click();
$("file-input").onchange = () => {
  addFiles($("file-input").files);
  $("file-input").value = "";
};
$("message-text").addEventListener("paste", (e) => {
  if (e.clipboardData.files.length) {
    e.preventDefault();
    addFiles(e.clipboardData.files);
  }
});
$("message-text").addEventListener("keydown", (e) => {
  if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
    e.preventDefault();
    send();
  }
});
let dragCount = 0;
document.addEventListener("dragenter", (e) => {
  if (!state.token || !e.dataTransfer.types.includes("Files")) return;
  e.preventDefault();
  dragCount++;
  $("drop-overlay").hidden = false;
});
document.addEventListener("dragover", (e) => {
  if (state.token && e.dataTransfer.types.includes("Files")) e.preventDefault();
});
document.addEventListener("dragleave", () => {
  if (--dragCount <= 0) {
    dragCount = 0;
    $("drop-overlay").hidden = true;
  }
});
document.addEventListener("drop", (e) => {
  dragCount = 0;
  $("drop-overlay").hidden = true;
  if (!state.token) return;
  e.preventDefault();
  addFiles(e.dataTransfer.files);
  if (!["messages", "files"].includes(state.view)) setView("messages");
});
const emojis = [
  "😀",
  "😊",
  "😂",
  "🥹",
  "🤔",
  "👍",
  "❤️",
  "🎉",
  "✨",
  "🔥",
  "✅",
  "📌",
  "📎",
  "☀️",
  "🌿",
  "☕",
  "🚀",
  "👀",
  "🙏",
  "💡",
];
for (const emoji of emojis)
  $("emoji-palette").append(
    button(emoji, () => {
      $("message-text").value += emoji;
      $("emoji-palette").hidden = true;
      $("message-text").focus();
    }),
  );
$("emoji").onclick = () =>
  ($("emoji-palette").hidden = !$("emoji-palette").hidden);
$("retention").onchange = () =>
  ($("custom-minutes").hidden = $("retention").value !== "custom");
function upload(file, text, ttl, concealed) {
  return new Promise((resolve, reject) => {
    const data = new FormData();
    data.append("text", text);
    data.append("device", device());
    data.append("ttl", String(ttl));
    data.append("concealed", String(concealed));
    data.append("file", file);
    const xhr = new XMLHttpRequest();
    xhr.open("POST", "/api/messages");
    xhr.setRequestHeader("Authorization", "Bearer " + state.token);
    xhr.upload.onprogress = (e) => {
      $("upload-progress").textContent = e.lengthComputable
        ? "正在上传 " +
          file.name +
          " · " +
          Math.floor((e.loaded / e.total) * 100) +
          "%"
        : "正在上传 " + file.name;
    };
    xhr.onload = () => {
      let b = {};
      try {
        b = JSON.parse(xhr.responseText);
      } catch {}
      if (xhr.status >= 200 && xhr.status < 300) resolve(b);
      else reject(Error(b.error || "上传失败（" + xhr.status + "）"));
    };
    xhr.onerror = () => reject(Error("网络中断，文件未发送，请重试"));
    xhr.timeout = 30 * 60 * 1000;
    xhr.ontimeout = () => reject(Error("上传超时，请检查网络后重试"));
    xhr.send(data);
  });
}
async function send() {
  if (state.busy) return;
  let text = $("message-text").value;
  const concealed = $("concealed-send").checked;
  if (!text.trim() && !state.files.length) return;
  let ttl =
    $("retention").value === "custom"
      ? Number($("custom-minutes").value) * 60
      : Number($("retention").value);
  if (
    !Number.isInteger(ttl) ||
    ttl < 0 ||
    ttl > 365 * 86400 ||
    ($("retention").value === "custom" && ttl < 60)
  ) {
    toast("自定义时间为 1–525600 分钟", true);
    return;
  }
  state.busy = true;
  for (const id of ["message-text", "retention", "custom-minutes"])
    $(id).disabled = true;
  $("send").disabled = true;
  $("attach").disabled = true;
  try {
    if (state.files.length) {
      while (state.files.length) {
        await upload(state.files[0], text, ttl, concealed);
        state.files.shift();
        text = "";
        $("message-text").value = "";
        renderAttachments();
      }
    } else {
      await json("/api/messages", {
        method: "POST",
        body: JSON.stringify({
          text,
          device: device(),
          ttl,
          ...(concealed ? { concealed: true } : {}),
        }),
      });
      $("message-text").value = "";
    }
    state.signature = "";
    await refresh();
    toast("已收好，其他设备会自动同步");
  } catch (e) {
    toast(e.message, true);
  } finally {
    state.busy = false;
    for (const id of ["message-text", "retention", "custom-minutes"])
      $(id).disabled = false;
    $("send").disabled = false;
    $("attach").disabled = false;
    $("upload-progress").textContent = "支持任意文件 · Ctrl / ⌘ + Enter 发送";
  }
}
$("send").onclick = send;
async function loadAdmin() {
  state.admin = await json("/api/admin");
  const s = state.admin.settings;
  $("public-registration").checked = s.public_registration;
  $("auto-load-images").checked = s.auto_load_images !== false;
  $("guest-hours").value = s.guest_hours;
  const idle = s.session_idle_minutes ?? 15;
  $("idle-unit").value =
    idle === 0 ? "never" : idle % 1440 === 0 ? "days" : "minutes";
  $("idle-value").value =
    idle === 0 ? 15 : idle % 1440 === 0 ? idle / 1440 : idle;
  $("idle-value").disabled = idle === 0;
  $("idle-value").max = $("idle-unit").value === "days" ? "3650" : "5256000";
  $("max-mb").value = s.max_file_mb;
  $("quota-mb").value = s.quota_mb;
  $("device-template").value = s.device_template;
  $("active-store").replaceChildren();
  for (const store of state.admin.stores) {
    const option = node("option", "", store.name + " · " + store.kind);
    option.value = store.id;
    $("active-store").append(option);
  }
  $("active-store").value = s.active_store;
  const list = $("users-list");
  list.replaceChildren();
  for (const u of state.admin.users) {
    const row = node("div", "user-row");
    const info = node("div");
    info.append(
      node("strong", "", u.name),
      node(
        "small",
        "",
        u.admin
          ? "永久管理员"
          : (u.expires * 1000 < Date.now() ? "已过期 · " : "有效至 ") +
              date(u.expires * 1000),
      ),
    );
    row.append(info);
    if (!u.admin)
      row.append(
        button(
          "延长有效期",
          async () => {
            const hours = prompt("从现在起有效多少小时？", "1");
            if (hours === null) return;
            if (
              !/^\d+$/.test(hours) ||
              Number(hours) < 1 ||
              Number(hours) > 8760
            )
              throw Error("请输入 1–8760 小时");
            await json("/api/admin/users/" + u.id, {
              method: "PUT",
              body: JSON.stringify({ hours: Number(hours) }),
            });
            await loadAdmin();
            toast("已更新有效期");
          },
          "secondary",
        ),
      );
    list.append(row);
  }
}
$("admin-form").onsubmit = async (e) => {
  e.preventDefault();
  await action(e.submitter, async () => {
    await json("/api/admin", {
      method: "PUT",
      body: JSON.stringify({
        public_registration: $("public-registration").checked,
        auto_load_images: $("auto-load-images").checked,
        guest_hours: Number($("guest-hours").value),
        session_idle_minutes:
          $("idle-unit").value === "never"
            ? 0
            : Number($("idle-value").value) *
              ($("idle-unit").value === "days" ? 1440 : 1),
        max_file_mb: Number($("max-mb").value),
        quota_mb: Number($("quota-mb").value),
        device_template: $("device-template").value,
        active_store: $("active-store").value,
      }),
    });
    browserSession.accept(state.token, await json("/api/session"));
    toast("设置已保存");
  });
};
$("idle-unit").onchange = () => {
  $("idle-value").disabled = $("idle-unit").value === "never";
  $("idle-value").max = $("idle-unit").value === "days" ? "3650" : "5256000";
};
function storeInput() {
  return {
    kind: $("storage-kind").value,
    name: $("storage-name").value,
    url: $("storage-url").value.trim(),
    root: $("storage-root").value.trim(),
    user: $("storage-user").value,
    password: $("storage-password").value,
    domain: $("storage-domain").value,
  };
}
$("storage-test").onclick = () =>
  action($("storage-test"), async () => {
    await json("/api/admin/storage/test", {
      method: "POST",
      body: JSON.stringify(storeInput()),
    });
    toast("读写测试通过");
  });
$("storage-form").onsubmit = async (e) => {
  e.preventDefault();
  await action(e.submitter, async () => {
    await json("/api/admin/storage", {
      method: "POST",
      body: JSON.stringify(storeInput()),
    });
    $("storage-password").value = "";
    await loadAdmin();
    toast("存储已添加，请在上方选择并保存为新文件存储位置");
  });
};
$("backup").onclick = () =>
  action($("backup"), async () => {
    const password = $("backup-password").value;
    if (password.length < 12) throw Error("备份密码至少 12 个字符");
    let output = null;
    if (window.showSaveFilePicker) {
      try {
        const handle = await window.showSaveFilePicker({
          suggestedName:
            "pocket-relay-" + new Date().toISOString().slice(0, 10) + ".prb",
          types: [
            {
              description: "Pocket Relay 加密备份",
              accept: { "application/octet-stream": [".prb"] },
            },
          ],
        });
        output = await handle.createWritable();
      } catch (e) {
        if (e.name === "AbortError") return;
        throw e;
      }
    }
    $("backup-progress").textContent = "正在创建加密备份，请保持页面打开…";
    try {
      const response = await api("/api/admin/backup", {
        method: "POST",
        body: JSON.stringify({ password }),
      });
      if (output) await response.body.pipeTo(output);
      else {
        const blob = await response.blob();
        const url = URL.createObjectURL(blob);
        const a = node("a");
        a.href = url;
        a.download = "pocket-relay-backup.prb";
        document.body.append(a);
        a.click();
        a.remove();
        setTimeout(() => URL.revokeObjectURL(url), 60000);
      }
      $("backup-password").value = "";
      $("backup-progress").textContent =
        "备份下载完成。请保存备份密码；远程文件本体需另行备份。";
    } catch (e) {
      if (output) await output.abort().catch(() => {});
      $("backup-progress").textContent = "备份未完成，请重试。";
      throw e;
    }
  });
$("restore-file").onchange = async () => {
  const file = $("restore-file").files[0];
  $("restore-file").value = "";
  if (!file) return;
  const password = $("backup-password").value;
  if (password.length < 12) {
    toast("请先填写此备份的密码", true);
    return;
  }
  if (!confirm("恢复将替换所有账户、设置和本地文件，并重启服务。确认恢复？"))
    return;
  $("backup-progress").textContent = "正在上传并验证备份，请勿关闭页面…";
  try {
    const result = await json("/api/admin/restore", {
      method: "POST",
      headers: {
        "X-Backup-Password": encodeURIComponent(password),
        "Content-Type": "application/octet-stream",
      },
      body: file,
    });
    $("backup-password").value = "";
    toast(result.message);
    logoutLocal();
    $("auth-error").textContent = "服务正在重启，请稍后使用备份中的账户登录。";
  } catch (e) {
    $("backup-progress").textContent = "恢复未完成，原有数据未替换。";
    toast(e.message, true);
  }
};

function expireSession(message = "长时间无操作，请重新验证") {
  const token = state.token;
  if (!token) return;
  logoutLocal();
  fetch("/api/logout", {
    method: "POST",
    headers: { Authorization: "Bearer " + token },
    keepalive: true,
  }).catch(() => {});
  $("auth-error").textContent = message;
}
async function flushActivity() {
  activityTimer = 0;
  if (!state.token || !activityPending || activityBusy) return;
  if (browserSession.expired()) {
    expireSession();
    return;
  }
  activityBusy = true;
  activityPending = false;
  try {
    const result = await json("/api/session/activity", { method: "POST" });
    browserSession.accept(state.token, result);
  } catch (e) {
    if (state.token) toast("活动同步失败，网络恢复后请重试。", true);
  } finally {
    activityBusy = false;
    if (activityPending && state.token)
      activityTimer = setTimeout(flushActivity, 5000);
  }
}
function recordActivity(event) {
  if (!event.isTrusted || document.hidden || !state.token) return;
  if (browserSession.expired()) {
    expireSession();
    return;
  }
  browserSession.input();
  activityPending = true;
  if (!activityTimer && !activityBusy)
    activityTimer = setTimeout(flushActivity, 5000);
}
for (const type of [
  "pointerdown",
  "pointermove",
  "keydown",
  "wheel",
  "touchstart",
  "input",
])
  document.addEventListener(type, recordActivity, { passive: true });
setInterval(() => {
  if (state.token && browserSession.expired()) expireSession();
}, 1000);
let policySyncBusy = false;
async function syncSessionPolicy() {
  if (
    !state.token ||
    document.hidden ||
    activityPending ||
    activityBusy ||
    policySyncBusy
  )
    return;
  policySyncBusy = true;
  try {
    const result = await json("/api/session");
    browserSession.accept(state.token, result);
  } catch {
  } finally {
    policySyncBusy = false;
  }
}
setInterval(syncSessionPolicy, 30000);
document.addEventListener("visibilitychange", () => {
  if (!document.hidden) syncSessionPolicy();
});
window.addEventListener("pageshow", (event) => {
  if (event.persisted && state.token)
    expireSession("页面已重新打开，请重新验证");
});

boot();
