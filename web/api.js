window.createRelayAPI = function ({getToken, onUnauthorized, notify, translate}) {
async function api(path, options = {}) {
  const requestToken = getToken();
  const headers = new Headers(options.headers);
  headers.set("X-Relay-Session", "1");
  if (getToken()) headers.set("Authorization", "Bearer " + getToken());
  if (
    options.body &&
    typeof options.body === "string" &&
    !headers.has("Content-Type")
  )
    headers.set("Content-Type", "application/json");
  const res = await fetch(path, { ...options, headers, cache: "no-store" });
  if (requestToken && requestToken !== getToken())
    throw Error("登录状态已改变，请重新操作");
  if (!res.ok && res.status !== 304) {
    const b = await res.json().catch(() => ({}));
    if (res.status === 401 && getToken()) {
      onUnauthorized();
      notify(b.error || translate("请重新验证"), true);
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
    notify(e.message, true);
  } finally {
    b.disabled = false;
  }
}
return { api, json, action };
};
