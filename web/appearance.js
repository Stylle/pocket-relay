/* Appearance contract v1. No credentials, business requests, or remote assets. */
(function () {
 "use strict";
 const root = document.documentElement;
 const id = root.dataset.appId;
 const key = id === "movie" ? "trm_theme" : id + "_theme";
 const choices = ["light", "dark", "green", "system"];
 const media = window.matchMedia ? window.matchMedia("(prefers-color-scheme: dark)") : null;
 let choice = root.dataset.defaultTheme || "light";
 try { const saved = localStorage.getItem(key); if (choices.includes(saved)) choice = saved; } catch {}
 function sync() {
  root.dataset.themeChoice = choice;
  root.dataset.theme = choice === "system" ? (media && media.matches ? "dark" : "light") : choice;
  document.querySelectorAll("[data-theme-select]").forEach(el => { el.value = choice; });
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) meta.content = root.dataset.theme === "dark" ? "#15181d" : choice === "green" ? "#f3f6f4" : "#f4f5f7";
 }
 function apply(value, persist) {
  if (!choices.includes(value)) return;
  choice = value; sync();
  if (persist) try { localStorage.setItem(key, value); } catch {}
  window.dispatchEvent(new CustomEvent("appearance-change", {detail: value}));
 }
 sync();
 if (media && media.addEventListener) media.addEventListener("change", sync);
 window.addEventListener("storage", event => {
  if (event.key === key) apply(event.newValue || root.dataset.defaultTheme || "light", false);
 });
 window.addEventListener("appearance-ready", sync);
 document.addEventListener("change", event => {
  if (event.target.matches("[data-theme-select]")) apply(event.target.value, true);
 });
 let about;
 function el(tag, text, className) {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  if (className) node.className = className;
  return node;
 }
 function showAbout() {
  if (!about) {
   about = el("dialog", undefined, "about-dialog");
   about.setAttribute("aria-labelledby", "about-title");
   const title = el("h2", "关于 " + root.dataset.appName); title.id = "about-title";
   const description = el("p", root.dataset.appDescription);
   const list = el("dl", undefined, "about-meta");
   const pairs = [
    ["版本", root.dataset.appVersion], ["许可", "MIT License"],
    ["版权", root.dataset.appCopyright], ["数据", "保存在部署者选择的本地或远程存储中。"],
    ["安全提示", "公网访问请使用 HTTPS；妥善保管账户、机器人凭据与备份。"]
   ];
   for (const pair of pairs) list.append(el("dt", pair[0]), el("dd", pair[1]));
   const full = el("a", "完整关于页面"); full.href = "/about.html";
   const source = el("a", "项目源码与使用说明");
   source.href = "https://github.com/Stylle/" + ({movie:"today-random-movie-web",relay:"pocket-relay",hub:"firmware-hub"}[id]);
   source.target = "_blank"; source.rel = "noopener noreferrer";
   const privacy = el("p", "本项目不提供端到端加密保证；消息隐私显示和发布账号管理不等于内容数字签名。第三方依赖遵循各自许可证，详情请查看项目说明。");
   const actions = el("div", undefined, "about-actions");
   const close = el("button", "关闭", "primary"); close.type = "button";
   close.addEventListener("click", () => about.close());
   actions.append(close); about.append(title, description, list, full, el("p", ""), source, privacy, actions);
   about.addEventListener("click", event => { if (event.target === about) {
    const rect = about.getBoundingClientRect();
    if (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom) about.close();
   } });
   document.body.append(about);
  }
  if (about.open) return;
  if (typeof about.showModal === "function") about.showModal();
  else { about.setAttribute("open",""); about.querySelector("button").focus(); }
 }
 let drawerHost, returnFocus;
 function closeDrawer() {
  if (!drawerHost) return;
  const host = drawerHost; drawerHost = null;
  host.classList.remove("drawer-open", "menu-open");
  host.querySelectorAll("[data-drawer-toggle]").forEach(button => button.setAttribute("aria-expanded","false"));
  const drawer = host.querySelector("[data-drawer]");
  if (drawer) { drawer.removeAttribute("aria-modal"); drawer.removeAttribute("role"); }
  if (returnFocus && returnFocus.isConnected) returnFocus.focus();
  returnFocus = null;
 }
 function openDrawer(host, trigger) {
  if (drawerHost === host) { closeDrawer(); return; }
  closeDrawer(); drawerHost = host; returnFocus = trigger;
  host.classList.add("drawer-open");
  host.querySelectorAll("[data-drawer-toggle]").forEach(button => button.setAttribute("aria-expanded","true"));
  const drawer = host.querySelector("[data-drawer]");
  if (drawer) {
   drawer.setAttribute("aria-modal","true"); drawer.setAttribute("role","dialog");
   const focus = drawer.querySelector("button,a,select,input,[tabindex]");
   if (focus) focus.focus();
  }
 }
 document.addEventListener("click", event => {
  const target = event.target.closest("button,a");
  if (!target) return;
  if (target.matches("[data-about-open]")) { event.preventDefault(); showAbout(); }
  if (target.matches("[data-drawer-toggle]")) {
   const host = target.closest("[data-drawer-host]");
   if (host) openDrawer(host,target);
  }
  if (target.matches("[data-drawer-close]") || (target.closest("[data-drawer] nav") && !target.matches("[data-about-open]"))) closeDrawer();
 });
 document.addEventListener("keydown", event => {
  if (!drawerHost || (about && about.open)) return;
  if (event.key === "Escape") { event.preventDefault(); closeDrawer(); return; }
  if (event.key !== "Tab") return;
  const nodes = Array.from(drawerHost.querySelectorAll("[data-drawer] button,[data-drawer] a,[data-drawer] input,[data-drawer] select")).filter(n => !n.disabled && n.getClientRects().length);
  if (!nodes.length) return;
  if (event.shiftKey && document.activeElement === nodes[0]) { event.preventDefault(); nodes[nodes.length-1].focus(); }
  else if (!event.shiftKey && document.activeElement === nodes[nodes.length-1]) { event.preventDefault(); nodes[0].focus(); }
 });
 window.addEventListener("hashchange",closeDrawer);
 if (window.matchMedia) {
  const desktop = window.matchMedia("(min-width:1025px)");
  if (desktop.addEventListener) desktop.addEventListener("change", event => { if (event.matches) closeDrawer(); });
 }
 function ready() { sync(); }
 if (document.readyState === "loading") document.addEventListener("DOMContentLoaded",ready,{once:true}); else ready();
})();

