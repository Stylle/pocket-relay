function uiText(el,key){if(window.AppI18n)window.AppI18n.bind(el,key);else el.textContent=key}
const tr = text => window.AppI18n ? window.AppI18n.t(text) : text;
function uiNode(tag,cls,key){const el=node(tag,cls,key);if(window.AppI18n)window.AppI18n.bind(el,key);return el}
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
  privacyRevision: null,
  revealing: new Set(),
  hasMore: false,
};
let toastTimer;
let booting = true;
let messageRequest;
let renderedRows = new Map();
const dateFormatter = new Intl.DateTimeFormat("zh-CN", {month:"short",day:"numeric",hour:"2-digit",minute:"2-digit"});
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
  if(window.AppI18n)window.AppI18n.bind(b,text);
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
function date(n) { return dateFormatter.format(n); }
async function api(path, options = {}) {
  const requestToken = state.token;
  const headers = new Headers(options.headers);
  headers.set("X-Relay-Session", "1");
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
      toast(b.error || tr("请重新验证"), true);
    }
    const error = Error(b.error || "请求失败，请检查网络（" + res.status + "）");
    error.status = res.status;
    throw error;
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
  uiText($("auth-title"),register
    ? state.status.setup
      ? tr("创建你的私人空间")
      : tr("创建临时空间")
    : tr("欢迎回来"));
  uiText($("auth-description"),register
    ? tr("先添加验证器，再用一组验证码确认绑定。")
    : tr("打开验证器，输入此刻的六位验证码。"));
  $("code-label").hidden = register;
  $("code").required = !register;
  uiText($("auth-submit"),register
    ? tr("生成验证器二维码 →")
    : tr("进入我的空间 →"));
  $("auth-switch").hidden =
    state.status.setup || !state.status.public_registration;
  uiText($("auth-switch"),register
    ? tr("已有账户？返回登录")
    : tr("还没有账户？创建临时空间"));
  $("guest-notice").textContent =
    register && !state.status.setup
      ? tr("此账户自完成注册起有效 ") +
        state.status.guest_hours +
        tr(" 小时，每个账户的数据独立。")
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
  $("session-loading").hidden = true;
  $("workspace").hidden = false;
  $("account-name").textContent = result.user.name;
  $("account-avatar").textContent = result.user.name.slice(0, 1);
  uiText($("account-role"),result.user.admin
    ? tr("永久管理员")
    : tr("有效至 ") + date(result.user.expires * 1000));
  $("admin-nav").hidden = !result.user.admin;
  $("device-name").value = device();
  $("sending-device").textContent = tr("来自 ") + device();
  await setView("messages");
}
async function boot() {
  let failed = false;
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
          throw e;
        }
      }
    }
    if (!state.status.setup) {
      try {
        const result = await json("/api/session/restore", { method: "POST" });
        await enterWorkspace(result);
        return;
      } catch (error) { if (error.status !== 401) throw error; }
    }
    authMode(state.status.setup ? "register" : "login");
  } catch (e) {
    failed = true;
    $("session-loading-text").textContent = tr("连接服务失败，请刷新重试。");
    $("session-retry").hidden = false;
  } finally {
    booting = false;
    $("session-loading").hidden = !failed;
    $("auth").hidden = failed || !!state.user;
  }
}
$("session-retry").onclick = () => location.reload();
document.querySelector("#relay-nav .brand").addEventListener("click", event => {
  if (!state.user) return;
  event.preventDefault();
  document.querySelector(".drawer-backdrop").click();
  setView("messages").catch(error => toast(error.message, true));
});
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
      uiText($("auth-submit"),tr("确认绑定并进入 →"));
      $("code").focus();
      return;
    }
    const result = await json(
      state.enrollment ? "/api/enroll/confirm" : "/api/login",
      {
        method: "POST",
        body: JSON.stringify(
          state.enrollment
            ? {
                enrollment: state.enrollment,
                code: $("code").value,
                remember: $("remember-login").checked,
              }
            : {
                name,
                code: $("code").value,
                remember: $("remember-login").checked,
              },
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
  cancelMessageRequest();
  renderedRows.clear();
  closeSearch();
  closeOriginal();
  state.privacyRevision = null;
  $("tg-token").value = "";
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
  $("auth").hidden = booting;
  $("timeline").replaceChildren();
  $("message-count").textContent = "0";
  $("message-text").value = "";
  renderAttachments();
  authMode("login");
}
$("logout").onclick = async () => {
  try {
    await json("/api/logout", { method: "POST" });
    logoutLocal();
  } catch (_) {
    toast(tr("退出未完成，请恢复网络后重试，避免记住状态仍然有效。"), true);
  }
};
document
  .querySelectorAll("[data-view]")
  .forEach(
    (b) =>
      (b.onclick = () =>
        setView(b.dataset.view).catch((e) => toast(e.message, true))),
  );
// Drawer, theme and About are handled by appearance.js; session state stays here.
function cancelMessageRequest() {
  if (messageRequest) messageRequest.abort();
  messageRequest = null;
  state.polling = false;
}
function showMessageLoading() {
  renderedRows.clear();
  const status = uiNode("p", "muted small", "正在加载…");
  status.setAttribute("role", "status");
  $("timeline").replaceChildren(status);
  $("timeline").setAttribute("aria-busy", "true");
}
async function setView(view) {
  if (view === state.view && state.token && state.signature && !state.filters) { closeSearch(); return; }
  cancelMessageRequest();
  closeSearch();
  closeOriginal();
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
  uiText($("search-status"),view === "messages"
      ? tr("历史已折叠；搜索默认覆盖全部记录。")
      : tr("搜索默认覆盖全部文字记录。"));
  state.view = view;
  $("open-search").hidden = view !== "all";
  $("workspace").classList.remove("menu-open", "drawer-open");
  document
    .querySelectorAll("[data-view]")
    .forEach((b) => b.classList.toggle("active", b.dataset.view === view));
  uiText($("view-title"),{
    messages: tr("本次消息"),
    all: tr("全部信息"),
    files: tr("文件信息"),
    settings: tr("设备设置"),
    admin: tr("管理后台"),
  }[view]);
  $("messages-view").hidden = !["messages", "all", "files"].includes(view);
  $("settings-view").hidden = view !== "settings";
  $("admin-view").hidden = view !== "admin";
  $("composer").hidden = !["messages", "all", "files"].includes(view);
  if (view === "admin") await loadAdmin();
  else if (["messages", "all", "files"].includes(view)) {
    state.signature = "";
    showMessageLoading();
    await refresh();
  }
}
$("device-form").onsubmit = (e) => {
  e.preventDefault();
  save("pr-device", $("device-name").value.trim());
  $("sending-device").textContent = tr("来自 ") + device();
  toast(tr("设备名称已保存"));
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
  toast(tr("已复制"));
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
    m.mime === "image/gif" ? tr("已复制静态首帧，保留动图请下载") : tr("图片已复制"),
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
function renderText(text) {
  const p = node("p", "message-text");
  for (const part of RelayMessageText.parse(text)) {
    if (part.type === "text") {
      p.append(document.createTextNode(part.text));
      continue;
    }
    const link = node("a", "message-link", part.text);
    link.href = part.href;
    if (part.type === "url") {
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      link.title = "在新标签页打开 " + part.text;
      link.onclick = () => {
        if (
          /MicroMessenger|QQ\/|DingTalk|Weibo|FBAN|FBAV|Instagram/i.test(
            navigator.userAgent,
          )
        )
          toast(
            tr("若无法打开，请在右上角选择“在浏览器中打开”，或复制链接到浏览器。"),
          );
      };
    } else
      link.title =
        part.type === "email" ? "打开邮件应用" : "拨打电话（由系统确认）";
    p.append(link);
  }
  return p;
}
let originalTrigger = null;
function closeOriginal() {
  $("image-viewer").hidden = true;
  $("image-viewer-image").removeAttribute("src");
  if (originalTrigger?.isConnected) originalTrigger.focus();
  originalTrigger = null;
}
async function openOriginal(m, trigger) {
  const token = state.token,
    generation = state.generation,
    revision = state.privacyRevision,
    url = await ticket(m.file_id);
  if (
    token !== state.token ||
    generation !== state.generation ||
    revision !== state.privacyRevision ||
    (m.concealed && !state.revealed.has(m.id))
  )
    return;
  originalTrigger = trigger;
  $("image-viewer-name").textContent = m.name;
  $("image-viewer-image").alt = m.name;
  $("image-viewer-image").src = url + "&inline=1";
  $("image-viewer").hidden = false;
  $("image-viewer-close").focus();
}
$("image-viewer-close").onclick = closeOriginal;
$("image-viewer").onclick = (e) => {
  if (e.target === $("image-viewer")) closeOriginal();
};
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape") closeOriginal();
  else if (e.key === "Tab" && !$("image-viewer").hidden) {
    e.preventDefault();
    $("image-viewer-close").focus();
  }
});
async function revealMessage(m) {
  if (state.revealing.has(m.id)) return;
  state.revealing.add(m.id);
  const token = state.token,
    generation = state.generation,
    revision = state.privacyRevision;
  try {
    const result = await json("/api/messages/" + m.id + "/text");
    if (
      token !== state.token ||
      generation !== state.generation ||
      revision !== state.privacyRevision
    )
      return;
    if ((result.privacy_version ?? 0) !== (m.privacy_version ?? 0)) {
      state.signature = "";
      await refresh();
      toast(tr("隐私状态已更新，请重新点击。"));
      return;
    }
    state.revealed.set(m.id, {
      text: result.text,
      version: m.privacy_version ?? 0,
    });
    renderMessages();
  } finally {
    state.revealing.delete(m.id);
  }
}
function privatePlaceholder(m) {
  const b = button("", () => revealMessage(m), "private-message");
  b.setAttribute("aria-label", "查看隐私消息");
  b.append(
    node(
      "span",
      m.file_id ? "image-placeholder" : "private-dots",
      m.file_id ? (isImage(m) ? "▧" : "▤") : "••••••",
    ),
  );
  b.append(
    node("small", "message-filename", m.file_id ? m.name : tr("点击查看完整消息")),
  );
  return b;
}
async function togglePrivacy(m) {
  const concealed = !m.concealed,
    previous = state.privacyRevision,
    token = state.token;
  state.generation++;
  state.polling = false;
  const result = await json("/api/messages/" + m.id + "/privacy", {
    method: "PUT",
    body: JSON.stringify({ concealed }),
  });
  if (token !== state.token) return;
  let text = "";
  if (!concealed) text = (await json("/api/messages/" + m.id + "/text")).text;
  if (token !== state.token) return;
  for (const message of [...state.messages, ...state.older])
    if (message.id === m.id)
      Object.assign(message, {
        concealed,
        text,
        privacy_version: result.privacy_version,
      });
  if (result.privacy_revision !== previous + 1) state.older = [];
  state.privacyRevision = result.privacy_revision;
  state.revealed.clear();
  state.loadedImages.clear();
  state.signature = "";
  closeOriginal();
  renderMessages();
  await refresh();
  toast(concealed ? tr("已设为隐私；已送达 TG 的副本不会撤回") : tr("已取消隐私"));
}
async function loadPreview(element, m, attempt = 0) {
  const token = state.token,
    generation = state.generation,
    revision = state.privacyRevision;
  const current = () =>
    token === state.token &&
    generation === state.generation &&
    revision === state.privacyRevision &&
    (!m.concealed || state.revealed.has(m.id));
  const retry = () => {
    if (attempt < 4 && current()) {
      setTimeout(
        () => {
          if (element.isConnected && current())
            loadPreview(element, m, attempt + 1);
        },
        500 * 2 ** attempt,
      );
    } else if (element.isConnected && current())
      element.replaceWith(
        uiNode("p", "muted small", "预览暂不可用或格式不受支持，请尝试下载。"),
      );
  };
  element.onerror = retry;
  try {
    const url = await ticket(m.file_id);
    if (element.isConnected && current()) element.src = url + "&inline=1";
  } catch {
    retry();
  }
}
function renderMessages() {
  const timeline = $("timeline");
  const seen = new Set(), now = Date.now() / 1000;
  const all = [...state.older, ...state.messages].filter(m => {
    if (seen.has(m.id) || (m.expires && m.expires <= now) || (state.view === "files" && !m.file_id)) return false;
    seen.add(m.id); return true;
  }).sort((a,b) => a.created-b.created || a.id.localeCompare(b.id));
  const nextRows = new Map();
  const rows = [];
  if (!all.length) {
    const empty = node("div", "empty");
    empty.append(
      node("span", "", "↗"),
      uiNode(
        "h2",
        "",
        state.view === "files"
          ? tr("这里，留给值得带走的文件。")
          : tr("第一条，留给自己。"),
      ),
      uiNode("p", "", "粘贴文字、拖入文件，换个设备接着看。"),
    );
    if (state.view === "messages") {
      empty.append(
        uiNode("p", "muted", "历史记录已折叠，本页只显示本次登录后发送的消息。"),
        button(tr("查看全部信息"), () => setView("all"), "secondary"),
      );
    } else if (state.filters) {
      empty.replaceChildren(uiNode("h2", "", "没有找到符合条件的消息。"));
    }
    rows.push(empty);
  }
  for (const m of all) {
    const key = JSON.stringify([m, state.privacyRevision, state.generation, state.autoLoadImages, state.loadedImages.has(m.id), state.revealed.get(m.id), window.AppI18n?.lang]);
    const existing = renderedRows.get(m.id);
    if (existing && existing.key === key) {
      rows.push(existing.row); nextRows.set(m.id, existing); continue;
    }
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
        node("span", "expiry-badge", date(m.expires * 1000) + tr(" 过期")),
      );
    const bubble = node("div", "bubble");
    if (
      state.revealed.has(m.id) &&
      state.revealed.get(m.id).version !== (m.privacy_version ?? 0)
    )
      state.revealed.delete(m.id);
    const hidden = m.concealed && !state.revealed.has(m.id);
    if (hidden) {
      bubble.append(privatePlaceholder(m));
      bubble.onclick = (e) => {
        if (e.target === bubble)
          revealMessage(m).catch((err) => toast(err.message, true));
      };
    }
    if (m.file_id && !hidden) {
      if (isImage(m)) {
        if (!state.autoLoadImages && !state.loadedImages.has(m.id)) {
          const lazy = button(
            "",
            () => {
              state.loadedImages.add(m.id);
              renderMessages();
            },
            "private-message",
          );
          lazy.setAttribute("aria-label", "加载图片");
          lazy.append(
            node("span", "image-placeholder", "▧"),
            node("small", "message-filename", m.name),
          );
          bubble.append(lazy);
        } else {
          const image = node("img", "message-image");
          image.alt = m.name;
          image.loading = "lazy";
          const preview = button(
            "",
            () => openOriginal(m, preview),
            "image-thumb",
          );
          preview.setAttribute("aria-label", "打开原图 " + m.name);
          preview.append(image, node("small", "message-filename", m.name));
          bubble.append(preview);
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
              (m.name.split(".").pop() || tr("文件")).toUpperCase(),
          ),
        );
        card.append(node("span", "file-icon", "▤"), info);
        bubble.append(card);
      }
    }
    const text = m.concealed ? state.revealed.get(m.id)?.text : m.text;
    if (!hidden && text) bubble.append(renderText(text));
    const actions = node("div", "message-actions");
    if (text) actions.append(button(tr("复制文字"), () => copyText(text)));
    actions.append(
      button(m.concealed ? tr("取消隐私") : tr("设为隐私"), () => togglePrivacy(m)),
    );
    if (m.concealed && state.revealed.has(m.id))
      actions.append(
        button(tr("收起"), () => {
          state.revealed.delete(m.id);
          renderMessages();
        }),
      );
    if (m.file_id) {
      if (isImage(m) && !hidden)
        actions.append(button(tr("复制图片"), () => copyImage(m)));
      actions.append(button(tr("下载 · ") + size(m.size), () => download(m)));
    }
    actions.append(
      button(tr("删除"), async () => {
        if (!confirm(tr("删除这条消息及附件？此操作无法撤销。"))) return;
        await json("/api/messages/" + m.id, { method: "DELETE" });
        state.older = state.older.filter((x) => x.id !== m.id);
        state.signature = "";
        await refresh();
        toast(tr("已删除"));
      }),
    );
    content.append(meta, bubble, actions);
    row.append(avatar, content);
    rows.push(row);
    nextRows.set(m.id, {key, row});
  }
  renderedRows = nextRows;
  // Keep unchanged rows mounted (especially video/audio); reconcile only moved/new nodes.
  for (let i = 0; i < rows.length; i++) {
    if (timeline.children[i] !== rows[i]) timeline.insertBefore(rows[i], timeline.children[i] || null);
  }
  while (timeline.children.length > rows.length) timeline.lastElementChild.remove();
  timeline.setAttribute("aria-busy", "false");
  $("load-older").hidden = !state.hasMore;
}
function closeSearch() {
  if ($("search-dialog").open) $("search-dialog").close();
}
$("open-search").onclick = () => {
  if (state.view !== "all" || !state.user) return;
  if (!$("search-dialog").open) $("search-dialog").showModal();
  $("search-text").focus();
};
$("close-search").onclick = closeSearch;
$("search-dialog").addEventListener("close", () => {
  if (!$("open-search").hidden && !$("workspace").hidden) $("open-search").focus();
});
$("search-dialog").addEventListener("click", event => {
  if (event.target !== $("search-dialog")) return;
  const rect = event.target.getBoundingClientRect();
  if (event.clientX < rect.left || event.clientX > rect.right ||
      event.clientY < rect.top || event.clientY > rect.bottom) closeSearch();
});
$("search-period").onchange = () => {
  const custom = $("search-period").value === "custom";
  $("search-custom").hidden = !custom;
  $("search-from").required = custom;
  $("search-to").required = custom;
};
$("clear-search").onclick = () =>
  setView("all").catch((e) => toast(e.message, true));
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
      toast(tr("请选择有效的起止时间"), true);
      return;
    }
    query.set("from", from);
    query.set("to", to);
  }
  cancelMessageRequest();
  state.filters = query.toString();
  closeSearch();
  // A global text search must not silently retain the file-only view filter.
  state.view = "all";
  document
    .querySelectorAll("[data-view]")
    .forEach((b) => b.classList.toggle("active", b.dataset.view === "all"));
  state.generation++;
  state.polling = false;
  state.older = [];
  state.messages = [];
  state.hasMore = false;
  state.signature = "";
  showMessageLoading();
  uiText($("view-title"),tr("搜索结果 · 全部记录"));
  uiText($("search-status"),tr("搜索范围：此账户的全部记录（含已折叠历史）。"));
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
  const controller = new AbortController();
  messageRequest = controller;
  state.polling = true;
  try {
    const response = await api("/api/messages?" + messageQuery(), {
      signal: controller.signal,
      headers:
        state.signature && state.etag ? { "If-None-Match": state.etag } : {},
    });
    if (generation !== state.generation) return;
    if (response.status === 304) {
      $("sync-state").classList.remove("offline");
      $("sync-state").lastChild.textContent = tr("已同步");
      return;
    }
    state.etag = response.headers.get("ETag") || "";
    const result = await response.json();
    if (generation !== state.generation) return;
    if (Number.isSafeInteger(result.current_count) && result.current_count >= 0) {
      $("message-count").textContent = String(result.current_count);
    } else if (state.view === "messages") {
      $("message-count").textContent = String(new Set([...result.messages, ...state.older].map(m => m.id)).size);
    }
    state.maxMB = result.max_file_mb;
    state.autoLoadImages = result.auto_load_images !== false;
    document.documentElement.dataset.messageSize = [
      "small",
      "medium",
      "large",
    ].includes(result.message_size)
      ? result.message_size
      : "medium";
    const privacyRevision = result.privacy_revision ?? 0;
    if (
      state.privacyRevision !== null &&
      state.privacyRevision !== privacyRevision
    ) {
      state.revealed.clear();
      state.loadedImages.clear();
      state.older = [];
      state.hasMore = !!result.has_more;
      closeOriginal();
    }
    state.privacyRevision = privacyRevision;
    for (const m of result.messages) {
      if (
        state.revealed.has(m.id) &&
        state.revealed.get(m.id).version !== (m.privacy_version ?? 0)
      )
        state.revealed.delete(m.id);
    }
    if (!state.older.length) state.hasMore = !!result.has_more;
    const sig = JSON.stringify([
      result.messages,
      state.autoLoadImages,
      state.hasMore,
      result.message_size,
      privacyRevision,
    ]);
    if (sig !== state.signature) {
      state.messages = result.messages;
      state.signature = sig;
      renderMessages();
    }
    $("sync-state").classList.remove("offline");
    $("sync-state").lastChild.textContent = tr("已同步");
  } catch (e) {
    if (generation !== state.generation || !state.token || controller.signal.aborted) return;
    $("timeline").setAttribute("aria-busy", "false");
    if (!state.signature) $("timeline").replaceChildren(uiNode("p","muted small","连接服务失败，请刷新重试。"));
    $("sync-state").classList.add("offline");
    $("sync-state").lastChild.textContent = tr("连接中断");
  } finally {
    if (generation === state.generation) state.polling = false;
    if (messageRequest === controller) messageRequest = null;
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
      toast(tr("已经到底啦"));
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
    toast(tr("文件正在发送，请稍后添加"), true);
    return;
  }
  for (const f of files) {
    if (f.size > state.maxMB * 1024 ** 2) {
      toast(f.name + tr(" 超过 ") + state.maxMB + tr(" MB 限制"), true);
      continue;
    }
    if (state.files.length >= 30) {
      toast(tr("每批最多 30 个文件，请分批发送"), true);
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
$("retention").onchange = () => {
  const custom = $("retention").value === "custom";
  $("custom-minutes").hidden = !custom;
  $("composer").classList.toggle("custom-retention", custom);
};
function sizeComposer() {
  const input = $("message-text");
  input.style.height = "auto";
  input.style.height = Math.min(input.scrollHeight, 96) + "px";
}
$("message-text").addEventListener("input", sizeComposer);
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
      uiText($("upload-progress"),e.lengthComputable
        ? tr("正在上传 ") +
          file.name +
          " · " +
          Math.floor((e.loaded / e.total) * 100) +
          "%"
        : tr("正在上传 ") + file.name);
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
    toast(tr("自定义时间为 1–525600 分钟"), true);
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
        sizeComposer();
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
      sizeComposer();
    }
    state.signature = "";
    await refresh();
    toast(tr("已收好，其他设备会自动同步"));
  } catch (e) {
    toast(e.message, true);
  } finally {
    state.busy = false;
    for (const id of ["message-text", "retention", "custom-minutes"])
      $(id).disabled = false;
    $("send").disabled = false;
    $("attach").disabled = false;
    uiText($("upload-progress"),tr("支持任意文件 · Ctrl / ⌘ + Enter 发送"));
  }
}
$("send").onclick = send;
let telegramRevision = "";
function showTelegramStatus(result) {
  const cfg = result.config || {};
  const pieces = [cfg.enabled ? "已开启" : "已关闭"];
  if (cfg.enabled)
    pieces.push(
      [cfg.receive ? "接收" : "", cfg.send ? "发送" : ""]
        .filter(Boolean)
        .join(" / "),
    );
  pieces.push(
    "待发送 " + (result.pending || 0),
    "失败 " + (result.failed || 0),
  );
  if (result.received) pieces.push("最近接收 " + date(result.received * 1000));
  if (result.sent) pieces.push("最近发送 " + date(result.sent * 1000));
  if (result.last_error) pieces.push(result.last_error);
  $("tg-status").textContent = pieces.join(" · ");
}
async function loadTelegram(prefetched) {
  const generation = state.generation;
  const result = prefetched || await json("/api/admin/telegram");
  if (generation !== state.generation || !state.admin || !state.user) return;
  const cfg = result.config || {};
  telegramRevision = result.revision || "";
  $("tg-enabled").checked = !!cfg.enabled;
  $("tg-receive").checked = result.configured ? !!cfg.receive : true;
  $("tg-send").checked = result.configured ? !!cfg.send : true;
  $("tg-private").checked = !!cfg.include_concealed;
  $("tg-receive-private").checked = cfg.receive_concealed !== false;
  $("tg-token").value = "";
  $("tg-token").placeholder = result.configured
    ? "Token 已加密保存；留空保持不变"
    : "从 @BotFather 获取 Token";
  $("tg-chat").value = cfg.chat_id || "";
  $("tg-user").replaceChildren();
  for (const user of state.admin.users) {
    const option = node(
      "option",
      "",
      user.name +
        (user.expires && user.expires * 1000 <= Date.now() ? tr("（已过期）") : ""),
    );
    option.value = user.id;
    $("tg-user").append(option);
  }
  $("tg-user").value = cfg.user_id || state.user.id;
  $("tg-detected").hidden = true;
  showTelegramStatus(result);
}
$("telegram-form").onsubmit = async (event) => {
  event.preventDefault();
  await action(event.submitter, async () => {
    await json("/api/admin/telegram", {
      method: "PUT",
      body: JSON.stringify({
        enabled: $("tg-enabled").checked,
        receive: $("tg-receive").checked,
        send: $("tg-send").checked,
        include_concealed: $("tg-private").checked,
        receive_concealed: $("tg-receive-private").checked,
        user_id: $("tg-user").value,
        chat_id: $("tg-chat").value.trim(),
        token: $("tg-token").value.trim(),
        revision: telegramRevision,
      }),
    });
    await loadTelegram();
    toast(tr("机器人设置已保存，已清空旧发送队列"));
  });
};
$("tg-test").onclick = () =>
  action($("tg-test"), async () => {
    const result = await json("/api/admin/telegram/test", {
      method: "POST",
      body: JSON.stringify({ token: $("tg-token").value.trim() }),
    });
    const chats = result.chats || [];
    $("tg-detected").replaceChildren();
    const placeholder = uiNode("option", "", "选择要绑定的私聊 ID");
    placeholder.value = "";
    $("tg-detected").append(placeholder);
    for (const id of chats) {
      const option = node("option", "", id);
      option.value = id;
      $("tg-detected").append(option);
    }
    $("tg-detected").hidden = !chats.length;
    if (chats.length === 1) {
      $("tg-chat").value = chats[0];
      $("tg-detected").value = chats[0];
    }
    $("tg-status").textContent = chats.length
      ? tr("连接正常，已检测到私聊 ID；请核对后保存。")
      : tr("连接正常。请在 Telegram 向 @") +
        (result.username || tr("你的机器人")) +
        tr(" 发送 /start 后重新检测。启用期间只显示已绑定 ID，修改绑定请先关闭同步。");
  });
$("tg-detected").onchange = () => {
  if ($("tg-detected").value) $("tg-chat").value = $("tg-detected").value;
};
$("tg-retry").onclick = () =>
  action($("tg-retry"), async () => {
    await json("/api/admin/telegram/retry", { method: "POST" });
    showTelegramStatus(await json("/api/admin/telegram"));
    toast(tr("已安排重试"));
  });
let telegramStatusBusy = false;
setInterval(async () => {
  if (
    !state.token ||
    state.view !== "admin" ||
    document.hidden ||
    telegramStatusBusy
  )
    return;
  telegramStatusBusy = true;
  try {
    showTelegramStatus(await json("/api/admin/telegram"));
  } catch {
  } finally {
    telegramStatusBusy = false;
  }
}, 10000);

async function loadAdmin() {
  const generation = state.generation;
  const [admin, telegram] = await Promise.all([json("/api/admin"), json("/api/admin/telegram")]);
  if (generation !== state.generation || !state.user) return;
  state.admin = admin;
  await loadTelegram(telegram);
  if (generation !== state.generation || !state.user) return;
  const s = state.admin.settings;
  $("public-registration").checked = s.public_registration;
  $("auto-load-images").checked = s.auto_load_images !== false;
  $("message-size").value = s.message_size || "medium";
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
          ? tr("永久管理员")
          : (u.expires * 1000 < Date.now() ? tr("已过期 · ") : tr("有效至 ")) +
              date(u.expires * 1000),
      ),
    );
    row.append(info);
    if (!u.admin)
      row.append(
        button(
          tr("延长有效期"),
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
            toast(tr("已更新有效期"));
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
        message_size: $("message-size").value,
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
    document.documentElement.dataset.messageSize = $("message-size").value;
    toast(tr("设置已保存"));
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
    toast(tr("读写测试通过"));
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
    toast(tr("存储已添加，请在上方选择并保存为新文件存储位置"));
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
    $("backup-progress").textContent = tr("正在创建加密备份，请保持页面打开…");
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
        tr("备份下载完成。请保存备份密码；远程文件本体需另行备份。");
    } catch (e) {
      if (output) await output.abort().catch(() => {});
      $("backup-progress").textContent = tr("备份未完成，请重试。");
      throw e;
    }
  });
$("restore-file").onchange = async () => {
  const file = $("restore-file").files[0];
  $("restore-file").value = "";
  if (!file) return;
  const password = $("backup-password").value;
  if (password.length < 12) {
    toast(tr("请先填写此备份的密码"), true);
    return;
  }
  if (!confirm(tr("恢复将替换所有账户、设置和本地文件，并重启服务。确认恢复？")))
    return;
  $("backup-progress").textContent = tr("正在上传并验证备份，请勿关闭页面…");
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
    $("auth-error").textContent = tr("服务正在重启，请稍后使用备份中的账户登录。");
  } catch (e) {
    $("backup-progress").textContent = tr("恢复未完成，原有数据未替换。");
    toast(e.message, true);
  }
};

function expireSession(
  message = browserSession.remembered
    ? "记住的登录已到期，请重新验证"
    : "长时间无操作，请重新验证",
) {
  const token = state.token;
  if (!token) return;
  logoutLocal();
  fetch("/api/logout", {
    method: "POST",
    headers: { Authorization: "Bearer " + token, "X-Relay-Session": "1" },
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
    if (state.token) toast(tr("活动同步失败，网络恢复后请重试。"), true);
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
  if (event.persisted && state.token && !browserSession.remembered)
    expireSession("页面已重新打开，请重新验证");
  else if (event.persisted && state.token) syncSessionPolicy();
});

boot();
