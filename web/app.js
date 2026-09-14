const $ = (id) => document.getElementById(id);

const state = {
  sessionId: null,
  session: null,
  configured: false,
  loading: false,
  sending: false,
  controller: null,
  providers: [],
  activeProviderId: "",
  editingProviderId: "",
};

function showNotice(message, kind = "info") {
  const notice = $("notice");
  if (!notice) return;
  notice.textContent = message || "";
  notice.className = `notice ${kind}`;
  notice.hidden = !message;
}

window.addEventListener("error", (event) => {
  setHealth("页面脚本错误", "bad");
  showNotice(`页面脚本加载失败：${event.message || "未知错误"}`, "error");
});
window.addEventListener("unhandledrejection", (event) => {
  const reason = event.reason instanceof Error ? event.reason.message : String(event.reason || "未知错误");
  showNotice(`页面操作失败：${reason}`, "error");
});

function setHealth(text, kind = "pending") {
  const health = $("health");
  if (health) {
    health.textContent = text;
    health.className = `pill ${kind}`;
  }
}

function setBusy(button, busy, busyText) {
  if (!button) return;
  if (busy) {
    button.dataset.originalText = button.textContent;
    button.textContent = busyText;
    button.disabled = true;
  } else {
    button.textContent = button.dataset.originalText || button.textContent;
    button.disabled = false;
  }
}

function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (character) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[character]));
}

function inlineMarkdown(value) {
  let output = escapeHTML(value);
  const codeSpans = [];
  output = output.replace(/`([^`\n]+)`/g, (_, code) => {
    const token = `\uE000${codeSpans.length}\uE001`;
    codeSpans.push(`<code>${code}</code>`);
    return token;
  });
  output = output.replace(/\[([^\]\n]+)\]\((https?:\/\/[^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer noopener">$1</a>');
  output = output.replace(/\*\*([^*\n]+)\*\*/g, "<strong>$1</strong>");
  output = output.replace(/__([^_\n]+)__/g, "<strong>$1</strong>");
  output = output.replace(/(^|[^\w])\*([^*\n]+)\*(?!\*)/g, "$1<em>$2</em>");
  output = output.replace(/(^|[^\w])_([^_\n]+)_(?!\w)/g, "$1<em>$2</em>");
  codeSpans.forEach((code, index) => {
    output = output.replace(`\uE000${index}\uE001`, code);
  });
  return output;
}

function appendMarkdown(parent, value) {
  const lines = String(value).split("\n");
  let list = null;
  let listType = "";
  lines.forEach((line) => {
    const heading = line.match(/^(#{1,6})[ \t]+(.*)$/);
    const bullet = line.match(/^[-*+][ \t]+(.*)$/);
    const ordered = line.match(/^\d+[.)][ \t]+(.*)$/);
    if (heading) {
      list = null;
      listType = "";
      const element = document.createElement(`h${Math.min(6, heading[1].length)}`);
      element.innerHTML = inlineMarkdown(heading[2]);
      parent.appendChild(element);
      return;
    }
    if (bullet || ordered) {
      const nextType = bullet ? "ul" : "ol";
      if (!list || listType !== nextType) {
        listType = nextType;
        list = document.createElement(nextType);
        parent.appendChild(list);
      }
      const item = document.createElement("li");
      item.innerHTML = inlineMarkdown((bullet || ordered)[1]);
      list.appendChild(item);
      return;
    }
    list = null;
    listType = "";
    if (line === "") {
      const spacer = document.createElement("div");
      spacer.className = "rich-spacer";
      parent.appendChild(spacer);
      return;
    }
    const paragraph = document.createElement("p");
    paragraph.innerHTML = inlineMarkdown(line);
    parent.appendChild(paragraph);
  });
}

function renderRichText(messageElement, value) {
  const content = messageElement.querySelector(".rich-content");
  if (!content) return;
  content.replaceChildren();
  const lines = String(value ?? "").replace(/\r\n/g, "\n").replace(/\r/g, "\n").split("\n");
  let prose = [];
  const flushProse = () => {
    if (prose.length > 0) appendMarkdown(content, prose.join("\n"));
    prose = [];
  };
  for (let index = 0; index < lines.length; index += 1) {
    const opening = lines[index].match(/^(`{3,})([^\n]*)$/);
    if (!opening) {
      prose.push(lines[index]);
      continue;
    }
    const closingPattern = new RegExp(`^\`{${opening[1].length},}[ \\t]*$`);
    let closingIndex = -1;
    for (let candidate = index + 1; candidate < lines.length; candidate += 1) {
      if (closingPattern.test(lines[candidate])) {
        closingIndex = candidate;
        break;
      }
    }
    if (closingIndex < 0) {
      prose.push(lines[index]);
      continue;
    }
    flushProse();
    const block = document.createElement("section");
    block.className = "code-block";
    const toolbar = document.createElement("div");
    toolbar.className = "code-toolbar";
    const language = document.createElement("span");
    language.className = "code-language";
    language.textContent = opening[2].trim() || "text";
    const copy = document.createElement("button");
    copy.type = "button";
    copy.className = "code-copy";
    copy.textContent = "复制代码";
    const raw = lines.slice(index + 1, closingIndex).join("\n");
    copy.addEventListener("click", () => copyText(raw, "代码已复制。"));
    toolbar.append(language, copy);
    const pre = document.createElement("pre");
    const code = document.createElement("code");
    code.textContent = raw;
    pre.appendChild(code);
    block.append(toolbar, pre);
    content.appendChild(block);
    index = closingIndex;
  }
  flushProse();
}

function setMessageText(messageElement, value, validate = false) {
  if (!messageElement) return;
  messageElement.dataset.rawText = String(value ?? "");
  renderRichText(messageElement, value);
  if (validate && messageElement.classList.contains("assistant")) {
    decorateShortXMessage(messageElement, messageElement.dataset.rawText).catch((error) => {
      showNotice(`ShortX 指令格式校验失败：${error.message}`, "error");
    });
  }
}

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
    cache: "no-store",
    headers: {
      Accept: "application/json",
      ...(options.body ? { "Content-Type": "application/json" } : {}),
      ...(options.headers || {}),
    },
  });
  if (!response.ok) {
    const text = (await response.text()).trim();
    throw new Error(text || `请求失败（HTTP ${response.status}）`);
  }
  return response;
}

async function validateShortXText(text) {
  return api("/api/shortx/validate", {
    method: "POST",
    body: JSON.stringify({ text }),
  }).then((response) => response.json());
}

function kindLabel(kind) {
  return kind === "rule" ? "自动指令" : "一键指令";
}

async function copyText(text, notice = "内容已复制。") {
  let copied = false;
  if (navigator.clipboard && window.isSecureContext !== false) {
    try {
      await navigator.clipboard.writeText(text);
      copied = true;
    } catch {
      copied = false;
    }
  }
  if (!copied) {
    const input = document.createElement("textarea");
    input.value = text;
    input.setAttribute("readonly", "");
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.appendChild(input);
    input.select();
    if (!document.execCommand("copy")) throw new Error("浏览器拒绝访问剪贴板");
    input.remove();
  }
  showNotice(notice, "info");
}

function downloadText(filename, text) {
  const blob = new Blob([text], { type: "text/plain;charset=utf-8" });
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  document.body.appendChild(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

async function importShortX(documentInfo) {
  if (typeof File !== "function") {
    await copyText(documentInfo.canonical);
    downloadText(documentInfo.filename, documentInfo.canonical);
    showNotice(`当前浏览器不支持文件分享，已复制并下载官方格式文件 ${documentInfo.filename}。`, "warn");
    return;
  }
  const file = new File([documentInfo.canonical], documentInfo.filename, { type: "text/plain;charset=utf-8" });
  if (navigator.share && navigator.canShare && navigator.canShare({ files: [file] })) {
    await navigator.share({ title: `导入 ShortX：${documentInfo.title}`, files: [file] });
    showNotice(`已打开系统分享面板，请选择 ShortX 导入这份${kindLabel(documentInfo.kind)}。`, "info");
    return;
  }
  if (navigator.share) {
    await navigator.share({ title: `导入 ShortX：${documentInfo.title}`, text: documentInfo.canonical });
    showNotice(`已打开系统分享面板，请选择 ShortX 导入这份${kindLabel(documentInfo.kind)}。`, "info");
    return;
  }
  await copyText(documentInfo.canonical);
  downloadText(documentInfo.filename, documentInfo.canonical);
  showNotice(`当前浏览器不支持直接分享文件，已复制并下载官方格式文件 ${documentInfo.filename}；请在 ShortX 中导入该文件。`, "warn");
}

function actionButton(text, handler, className = "") {
  const button = document.createElement("button");
  button.type = "button";
  button.className = className;
  button.textContent = text;
  button.addEventListener("click", async () => {
    button.disabled = true;
    try {
      await handler();
    } catch (error) {
      if (error.name !== "AbortError") showNotice(`操作失败：${error.message}`, "error");
    } finally {
      button.disabled = false;
    }
  });
  return button;
}

function appendShortXActions(target, documentInfo, messageLevel = false) {
  if (!target || target.querySelector(".shortx-copy")) return;
  const label = kindLabel(documentInfo.kind);
  const copy = actionButton(`复制${label}`, () => copyText(documentInfo.canonical, `${label}已按 ShortX 官方格式复制。`), "shortx-copy");
  const importButton = actionButton(`一键导入 ShortX`, () => importShortX(documentInfo), "shortx-import");
  if (messageLevel) {
    target.classList.add("message-actions");
  }
  target.append(copy, importButton);
}

async function decorateShortXMessage(messageElement, text) {
  if (!messageElement || messageElement.dataset.shortxDecorated === "1" || !text.trim()) return;
  messageElement.dataset.shortxDecorated = "1";
  const blocks = [...messageElement.querySelectorAll(".code-block")];
  for (const block of blocks) {
    const code = block.querySelector("code");
    if (!code) continue;
    const result = await validateShortXText(code.textContent || "");
    if (result.valid) {
      appendShortXActions(block.querySelector(".code-toolbar"), result);
      return;
    }
  }
  const result = await validateShortXText(text);
  if (result.valid) {
    const actions = document.createElement("div");
    actions.className = "message-actions";
    appendShortXActions(actions, result, true);
    messageElement.appendChild(actions);
  }
}

function addMessage(role, text = "", validate = role === "assistant") {
  const element = document.createElement("article");
  element.className = `message ${role}`;
  const content = document.createElement("div");
  content.className = "rich-content";
  element.appendChild(content);
  $("chat").appendChild(element);
  setMessageText(element, text, validate);
  $("chat").scrollTop = $("chat").scrollHeight;
  return element;
}

function clearChat(message = "") {
  $("chat").replaceChildren();
  if (message) {
    const empty = document.createElement("p");
    empty.className = "empty-state";
    empty.textContent = message;
    $("chat").appendChild(empty);
  }
}

function setConfigured(configured) {
  state.configured = Boolean(configured);
  const message = $("message");
  const sendButton = $("sendButton");
  if (message) message.disabled = !state.configured;
  if (sendButton) sendButton.disabled = !state.configured || state.sending;
}

function providerById(id) {
  return state.providers.find((provider) => provider.id === id) || null;
}

function providerModels(provider) {
  if (!provider) return [];
  const models = Array.isArray(provider.models) ? provider.models.filter((model) => model && model.id) : [];
  if (provider.defaultModelId && !models.some((model) => model.id === provider.defaultModelId)) {
    models.unshift({ id: provider.defaultModelId, enabled: true });
  }
  return models;
}

function selectedProviderId() {
  return $("providerSelect")?.value || state.session?.providerId || state.activeProviderId || "";
}

function renderProviders() {
  const select = $("providerSelect");
  const list = $("providerList");
  if (select) {
    select.replaceChildren();
    state.providers.forEach((provider) => {
      const option = document.createElement("option");
      option.value = provider.id;
      option.textContent = provider.name || provider.id;
      select.appendChild(option);
    });
    const preferred = state.session?.providerId || state.activeProviderId || state.providers[0]?.id || "";
    if (preferred) select.value = preferred;
    select.disabled = state.providers.length === 0;
  }
  if (!list) return;
  list.replaceChildren();
  if (state.providers.length === 0) {
    const empty = document.createElement("p");
    empty.className = "sidebar-empty";
    empty.textContent = "还没有模型服务商，请新增一个。";
    list.appendChild(empty);
    return;
  }
  state.providers.forEach((provider) => {
    const button = document.createElement("button");
    button.type = "button";
    button.className = `provider-item ${provider.id === state.editingProviderId ? "active" : ""}`;
    const title = document.createElement("strong");
    title.textContent = provider.name || provider.id;
    const detail = document.createElement("small");
    detail.textContent = `${provider.id} · ${provider.keyConfigured ? "Key 已配置" : "缺少 Key"}`;
    button.append(title, detail);
    button.addEventListener("click", () => editProvider(provider.id));
    list.appendChild(button);
  });
}

function renderModels(providerId = selectedProviderId(), preferredModel = "") {
  const select = $("model");
  const hint = $("modelEmpty");
  if (!select) return;
  const models = providerModels(providerById(providerId));
  select.replaceChildren();
  models.forEach((model) => {
    const option = document.createElement("option");
    option.value = model.id;
    option.textContent = model.id;
    option.disabled = model.enabled === false;
    select.appendChild(option);
  });
  const selected = preferredModel || state.session?.modelId || providerById(providerId)?.defaultModelId || models[0]?.id || "";
  if (selected) select.value = selected;
  select.disabled = models.length === 0;
  if (hint) hint.hidden = models.length !== 0;
}

function renderSessions(items) {
  const list = $("sessions");
  if (!list) return;
  list.replaceChildren();
  if (!Array.isArray(items) || items.length === 0) {
    const empty = document.createElement("p");
    empty.className = "sidebar-empty";
    empty.textContent = "还没有会话";
    list.appendChild(empty);
    return;
  }
  items.forEach((item) => {
    const row = document.createElement("div");
    row.className = `session-row ${item.id === state.sessionId ? "active" : ""}`;
    const openButton = document.createElement("button");
    openButton.className = "session-open";
    openButton.type = "button";
    openButton.textContent = item.title || "未命名会话";
    openButton.title = item.title || item.id;
    openButton.addEventListener("click", () => openSession(item.id));
    const deleteButton = document.createElement("button");
    deleteButton.className = "session-delete";
    deleteButton.type = "button";
    deleteButton.textContent = "×";
    deleteButton.title = "删除会话";
    deleteButton.addEventListener("click", () => deleteSession(item.id));
    row.append(openButton, deleteButton);
    list.appendChild(row);
  });
}

function setSidebar(open) {
  $("sessionSidebar")?.classList.toggle("mobile-open", open);
}

function showSettings(open = true) {
  const panel = $("settingsPanel");
  if (panel) panel.hidden = !open;
  if (open) {
    renderProviders();
    if (state.editingProviderId) editProvider(state.editingProviderId);
    else if (state.providers[0]) editProvider(state.activeProviderId || state.providers[0].id);
  }
}

function showDialog(dialog) {
  if (!dialog || dialog.open) return;
  if (typeof dialog.showModal === "function") dialog.showModal();
  else dialog.setAttribute("open", "");
}
function closeDialog(dialog) {
  if (!dialog) return;
  if (typeof dialog.close === "function") dialog.close();
  else dialog.removeAttribute("open");
}

function editProvider(id) {
  const provider = providerById(id);
  if (!provider) return;
  state.editingProviderId = id;
  $("providerId").value = provider.id;
  $("providerName").value = provider.name || "";
  $("providerEndpoint").value = provider.endpoint || "";
  $("providerProtocol").value = provider.protocol || "openai";
  $("providerDefaultModel").value = provider.defaultModelId || "";
  $("providerModels").value = providerModels(provider).map((model) => model.id).join("\n");
  $("providerKey").value = "";
  $("providerKeyHint").textContent = provider.keyConfigured
    ? "Key 已存在。留空会保留当前文件；输入新值会原子替换，权限保持 600。"
    : "尚未配置 Key。保存后写入独立的 provider key 文件。";
  renderProviders();
}

function newProvider() {
  state.editingProviderId = "";
  ["providerId", "providerName", "providerEndpoint", "providerDefaultModel", "providerModels", "providerKey"].forEach((id) => {
    if ($(id)) $(id).value = "";
  });
  $("providerProtocol").value = "openai";
  $("providerKeyHint").textContent = "新服务商需要 Key；Key 只写入独立本地文件，不会回显。";
  renderProviders();
}

function applySession(session) {
  state.session = session || null;
  state.sessionId = session?.id || state.sessionId;
  const providerId = session?.providerId || state.activeProviderId || state.providers[0]?.id || "";
  if ($("providerSelect") && providerId) $("providerSelect").value = providerId;
  renderModels(providerId, session?.modelId || "");
  const reasoning = session?.reasoningLevel || "xhigh";
  if ($("reasoning")) $("reasoning").value = reasoning;
}

async function patchSession(settings) {
  if (!state.sessionId) return;
  const session = await api(`/api/sessions/${encodeURIComponent(state.sessionId)}`, {
    method: "PATCH",
    body: JSON.stringify(settings),
  }).then((response) => response.json());
  applySession(session);
}

async function openSession(id) {
  try {
    const session = await api(`/api/sessions/${encodeURIComponent(id)}`).then((response) => response.json());
    applySession(session);
    setSidebar(false);
    clearChat(session.messages?.length ? "" : "输入需求开始生成 ShortX 指令。");
    (session.messages || []).forEach((message) => addMessage(message.role, message.content));
    await refresh();
  } catch (error) {
    showNotice(`打开会话失败：${error.message}`, "error");
  }
}

async function ensureSession() {
  if (state.sessionId) return;
  const items = await api("/api/sessions").then((response) => response.json());
  if (items.length > 0) {
    await openSession(items[0].id);
    return;
  }
  const session = await api("/api/sessions", { method: "POST" }).then((response) => response.json());
  applySession(session);
  renderSessions([session]);
  clearChat("输入需求开始生成 ShortX 指令。");
}

async function createSession() {
  try {
    const session = await api("/api/sessions", { method: "POST" }).then((response) => response.json());
    await openSession(session.id);
  } catch (error) {
    showNotice(`新建会话失败：${error.message}`, "error");
  }
}

async function deleteSession(id) {
  if (!window.confirm("确定删除这个加密会话吗？删除后无法恢复。")) return;
  try {
    await api(`/api/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
    if (state.sessionId === id) {
      state.sessionId = null;
      state.session = null;
      clearChat("输入需求开始生成 ShortX 指令。");
      await ensureSession();
    }
    await refresh();
    showNotice("会话已删除。", "info");
  } catch (error) {
    showNotice(`删除会话失败：${error.message}`, "error");
  }
}

async function loadProviders() {
  const payload = await api("/api/providers").then((response) => response.json());
  state.providers = Array.isArray(payload.providers) ? payload.providers : [];
  state.activeProviderId = payload.activeProviderId || state.providers[0]?.id || "";
  renderProviders();
}

async function refresh({ openSettings = false } = {}) {
  if (state.loading) return;
  state.loading = true;
  setHealth("连接中", "pending");
  try {
    const health = await api("/health").then((response) => response.json());
    if (health.status !== "ok") throw new Error("引擎健康检查未通过");
    const buildVersion = $("buildVersion");
    if (buildVersion && health.version) buildVersion.textContent = `v${health.version}`;
    await loadProviders();
    if (state.sessionId) {
      const session = await api(`/api/sessions/${encodeURIComponent(state.sessionId)}`).then((response) => response.json());
      applySession(session);
    }
    let ready = Boolean(health.configured);
    const sessionProvider = state.session?.providerId ? providerById(state.session.providerId) : null;
    if (state.session?.providerId) ready = Boolean(sessionProvider?.keyConfigured);
    setConfigured(ready);
    setHealth(ready ? "已连接" : "需要配置", ready ? "ok" : "warn");
    renderModels(selectedProviderId(), state.session?.modelId || "");
    const sessions = await api("/api/sessions").then((response) => response.json());
    renderSessions(sessions);
    if (!ready || state.providers.length === 0) {
      showNotice("服务已启动，但还没有完整模型配置。请在“模型设置”中新增服务商并保存本地 Key。", "warn");
      if (openSettings || state.providers.length === 0) showSettings(true);
    } else {
      showNotice("");
      if (openSettings) showSettings(true);
    }
  } catch (error) {
    setConfigured(false);
    setHealth("服务不可用", "bad");
    showNotice(`无法连接本地 AI 引擎：${error.message}`, "error");
  } finally {
    state.loading = false;
  }
}

async function saveProvider(event) {
  event.preventDefault();
  const button = $("providerForm")?.querySelector("button[type=submit]");
  const models = $("providerModels").value.split("\n").map((id) => id.trim()).filter(Boolean).map((id) => ({ id, enabled: true }));
  const defaultModelId = $("providerDefaultModel").value.trim();
  if (!defaultModelId && models.length === 0) {
    showNotice("至少填写一个默认模型 ID 或模型列表。", "error");
    return;
  }
  setBusy(button, true, "保存中…");
  try {
    const body = {
      id: $("providerId").value.trim(),
      name: $("providerName").value.trim(),
      endpoint: $("providerEndpoint").value.trim(),
      protocol: $("providerProtocol").value,
      defaultModelId,
      models,
      key: $("providerKey").value,
    };
    const result = await api("/api/providers", { method: "POST", body: JSON.stringify(body) }).then((response) => response.json());
    await api("/api/config/reload", { method: "POST" });
    await loadProviders();
    state.editingProviderId = result.id;
    editProvider(result.id);
    await refresh();
    showNotice("服务商配置和本地 Key 已保存，并已从磁盘重新加载。", "info");
  } catch (error) {
    showNotice(`保存服务商失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

async function selectSessionProvider() {
  const providerId = $("providerSelect").value;
  const provider = providerById(providerId);
  renderModels(providerId, provider?.defaultModelId || "");
  if (!provider) return;
  try {
    await api("/api/providers/select", {
      method: "POST",
      body: JSON.stringify({ id: providerId }),
    });
    await patchSession({ providerId, modelId: provider.defaultModelId || providerModels(provider)[0]?.id || "", reasoningLevel: $("reasoning").value || "xhigh" });
    showNotice(`当前会话已切换到 ${provider.name || provider.id}。`, "info");
    setConfigured(Boolean(provider.keyConfigured));
    setHealth(provider.keyConfigured ? "已连接" : "需要配置", provider.keyConfigured ? "ok" : "warn");
  } catch (error) {
    showNotice(`切换服务商失败：${error.message}`, "error");
  }
}

async function deleteProvider() {
  const id = $("providerId").value.trim();
  if (!id) return;
  if (!window.confirm(`确定删除服务商 ${id} 及其独立 Key 文件吗？`)) return;
  try {
    await api(`/api/providers/${encodeURIComponent(id)}`, { method: "DELETE" });
    await api("/api/config/reload", { method: "POST" });
    state.editingProviderId = "";
    await refresh({ openSettings: true });
    showNotice(`服务商 ${id} 及其 Key 文件已删除。`, "info");
  } catch (error) {
    showNotice(`删除服务商失败：${error.message}`, "error");
  }
}

async function reloadConfig() {
  const button = $("refreshConfig");
  setBusy(button, true, "刷新中…");
  try {
    await api("/api/config/reload", { method: "POST" });
    await refresh({ openSettings: true });
    showNotice("配置已从磁盘重新读取；当前 provider 使用其对应的本地 Key。", "info");
  } catch (error) {
    showNotice(`刷新配置失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

async function restartEngine() {
  if (!window.confirm("重启引擎会短暂断开本地页面连接，是否继续？")) return;
  const button = $("restartEngine");
  setBusy(button, true, "重启中…");
  try {
    const before = await api("/health").then((response) => response.json());
    await api("/api/engine/restart", { method: "POST" });
    showNotice("引擎正在真实重启，等待新实例恢复…", "info");
    let lastError = null;
    for (let attempt = 0; attempt < 30; attempt += 1) {
      await new Promise((resolve) => setTimeout(resolve, 300));
      try {
        const health = await api("/health").then((response) => response.json());
        if (health.status === "ok" && health.instanceId && health.instanceId !== before.instanceId) {
          await refresh({ openSettings: true });
          showNotice("引擎已重启，并已重新读取磁盘配置和对应 provider Key。", "info");
          return;
        }
      } catch (error) {
        lastError = error;
      }
    }
    throw lastError || new Error("新引擎实例未恢复");
  } catch (error) {
    showNotice(`重启后健康检查失败：${error.message}`, "error");
  } finally {
    setBusy(button, false);
  }
}

async function openDeviceCapabilities() {
  const output = $("deviceOutput");
  if (output) output.textContent = "读取中…";
  showDialog($("deviceDialog"));
  try {
    const capabilities = await api("/api/device-capabilities").then((response) => response.json());
    if (output) output.textContent = JSON.stringify(capabilities, null, 2);
  } catch (error) {
    if (output) output.textContent = `读取失败：${error.message}`;
    showNotice(`读取设备能力失败：${error.message}`, "error");
  }
}

async function sendMessage(event) {
  event.preventDefault();
  if (state.sending) return;
  if (!state.configured) {
    showNotice("请先在模型设置中保存服务商和本地 Key。", "warn");
    showSettings(true);
    return;
  }
  const message = $("message").value.trim();
  if (!message || !$("model").value) return;
  state.sending = true;
  state.controller = new AbortController();
  $("message").value = "";
  setConfigured(true);
  setBusy($("sendButton"), true, "生成中…");
  let userMessage = null;
  let assistantMessage = null;
  let assistantText = "";
  let received = false;
  try {
    await ensureSession();
    userMessage = addMessage("user", message);
    assistantMessage = addMessage("assistant", "正在生成…", false);
    const response = await api("/api/chat", {
      method: "POST",
      signal: state.controller.signal,
      body: JSON.stringify({
        sessionId: state.sessionId,
        providerId: selectedProviderId(),
        message,
        modelId: $("model").value,
        reasoningLevel: $("reasoning").value || "xhigh",
      }),
    });
    if (!response.body) throw new Error("浏览器不支持流式响应");
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    setMessageText(assistantMessage, "", false);
    while (true) {
      const result = await reader.read();
      buffer += decoder.decode(result.value || new Uint8Array(), { stream: !result.done });
      const events = buffer.split("\n\n");
      buffer = events.pop() || "";
      for (const eventText of events) {
        const data = eventText.split("\n").filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trim()).join("\n");
        if (!data) continue;
        let eventData;
        try { eventData = JSON.parse(data); } catch { continue; }
        if (eventData.type === "delta") {
          received = true;
          assistantText += eventData.content || "";
          setMessageText(assistantMessage, assistantText, false);
          $("chat").scrollTop = $("chat").scrollHeight;
        } else if (eventData.type === "error") {
          throw new Error(eventData.message || "模型服务返回错误");
        } else if (eventData.type === "done" && eventData.sessionId) {
          state.sessionId = eventData.sessionId;
        }
      }
      if (result.done) break;
    }
    if (!received) {
      setMessageText(assistantMessage, "模型没有返回内容。", false);
      showNotice("模型服务已响应，但没有返回文本。", "warn");
    } else {
      setMessageText(assistantMessage, assistantText, true);
      showNotice("");
    }
    await refresh();
  } catch (error) {
    if (assistantMessage) {
      const message = error.name === "AbortError" ? "已取消生成。" : `生成失败：${error.message}`;
      setMessageText(assistantMessage, message, false);
      assistantMessage.classList.add("error-message");
    }
    if (error.name !== "AbortError") showNotice(`模型请求失败：${error.message}`, "error");
    if (userMessage) userMessage.scrollIntoView({ block: "nearest" });
  } finally {
    state.sending = false;
    state.controller = null;
    setBusy($("sendButton"), false);
    setConfigured(state.configured);
  }
}

function on(id, event, handler) {
  const element = $(id);
  if (element) element.addEventListener(event, handler);
}

on("newSession", "click", async () => { await createSession(); setSidebar(false); });
on("sessionsButton", "click", () => setSidebar(true));
on("closeSessions", "click", () => setSidebar(false));
on("deviceCapabilities", "click", openDeviceCapabilities);
on("closeDevice", "click", () => closeDialog($("deviceDialog")));
on("settings", "click", () => showSettings(true));
on("closeSettings", "click", () => showSettings(false));
on("newProvider", "click", newProvider);
on("providerForm", "submit", saveProvider);
on("deleteProvider", "click", deleteProvider);
on("refreshConfig", "click", reloadConfig);
on("restartEngine", "click", restartEngine);
on("providerSelect", "change", selectSessionProvider);
on("model", "change", async () => {
  try { await patchSession({ modelId: $("model").value }); } catch (error) { showNotice(`保存模型选择失败：${error.message}`, "error"); }
});
on("reasoning", "change", async () => {
  try { await patchSession({ reasoningLevel: $("reasoning").value || "xhigh" }); } catch (error) { showNotice(`保存推理等级失败：${error.message}`, "error"); }
});
on("composer", "submit", sendMessage);

clearChat("输入需求开始生成 ShortX 指令。");
refresh({ openSettings: true }).then(ensureSession).catch((error) => showNotice(`初始化会话失败：${error.message}`, "error"));
