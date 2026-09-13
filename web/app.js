const $ = (id) => document.getElementById(id);

const state = {
  sessionId: null,
  configured: false,
  loading: false,
  sending: false,
  controller: null,
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
  health.textContent = text;
  health.className = `pill ${kind}`;
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

async function api(path, options = {}) {
  const response = await fetch(path, {
    ...options,
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

function parseJSONLines(value) {
  return value
    .split("\n")
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      try {
        return JSON.parse(line);
      } catch {
        return null;
      }
    })
    .filter(Boolean);
}

function addMessage(role, text = "") {
  const element = document.createElement("article");
  element.className = `message ${role}`;
  element.dataset.role = role;
  element.textContent = text;
  $("chat").appendChild(element);
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
  const composer = $("composer");
  const message = $("message");
  const sendButton = $("sendButton");
  if (composer) composer.classList.toggle("disabled", !state.configured);
  if (message) message.disabled = !state.configured;
  if (sendButton) sendButton.disabled = !state.configured || state.sending;
}

function renderModels(payload) {
  const models = Array.isArray(payload.models) ? payload.models : [];
  const select = $("model");
  select.replaceChildren();
  models.forEach((model) => {
    if (!model || !model.id) return;
    const option = document.createElement("option");
    option.value = model.id;
    option.textContent = model.id;
    option.disabled = model.enabled === false;
    select.appendChild(option);
  });
  if (payload.defaultModelId) select.value = payload.defaultModelId;
  select.disabled = models.length === 0;
  const modelEmpty = $("modelEmpty");
  if (modelEmpty) modelEmpty.hidden = models.length !== 0;
}

function renderSessions(items) {
  const list = $("sessions");
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

async function refresh({ openSettings = false } = {}) {
  if (state.loading) return;
  state.loading = true;
  setHealth("连接中", "pending");
  try {
    const health = await api("/health").then((response) => response.json());
    if (health.status !== "ok") throw new Error("引擎健康检查未通过");
    const buildVersion = $("buildVersion");
    if (buildVersion && health.version) buildVersion.textContent = `v${health.version}`;
    setConfigured(health.configured);
    setHealth(health.configured ? "已连接" : "需要配置", health.configured ? "ok" : "warn");

    const [models, sessions] = await Promise.all([
      api("/api/models").then((response) => response.json()),
      api("/api/sessions").then((response) => response.json()),
    ]);
    renderModels(models);
    renderSessions(sessions);

    if (!health.configured) {
      showNotice("服务已启动，但还没有模型配置。请点击“模型设置”，填写服务商、端点和模型。", "warn");
      const configDialog = $("configDialog");
      if (openSettings || !configDialog || !configDialog.open) await openSettingsDialog();
    } else if (!models.models || models.models.length === 0) {
      showNotice("服务已连接，但没有可用模型。请在“模型设置”中填写默认模型 ID。", "warn");
    } else {
      showNotice("");
    }
  } catch (error) {
    setConfigured(false);
    setHealth("服务不可用", "bad");
    showNotice(`无法连接本地 AI 引擎：${error.message}`, "error");
  } finally {
    state.loading = false;
  }
}

async function openSession(id) {
  try {
    const session = await api(`/api/sessions/${encodeURIComponent(id)}`).then((response) => response.json());
    state.sessionId = session.id;
    clearChat(session.messages?.length ? "" : "输入需求开始生成 ShortX 指令。" );
    (session.messages || []).forEach((message) => addMessage(message.role, message.content));
    await refresh();
  } catch (error) {
    showNotice(`打开会话失败：${error.message}`, "error");
  }
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
      clearChat("输入需求开始生成 ShortX 指令。");
    }
    await refresh();
    showNotice("会话已删除。", "info");
  } catch (error) {
    showNotice(`删除会话失败：${error.message}`, "error");
  }
}

function setConfigForm(config) {
  $("provider").value = config.provider || "";
  $("endpoint").value = config.endpoint || "";
  $("protocol").value = config.protocol || "openai";
  $("defaultModel").value = config.defaultModelId || "";
  $("models").value = (config.models || []).map((model) => model.id).join("\n");
  $("configReasoning").value = config.reasoningLevel || "medium";
  $("apiKey").value = "";
  const keyHint = $("keyHint");
  if (keyHint) {
    keyHint.textContent = config.apiKeyEnv
      ? `当前使用环境变量 ${config.apiKeyEnv}（页面不会回显密钥）`
      : config.apiKey === "configured"
        ? "当前已有密钥，留空将保留现有密钥"
        : "推荐使用 ShortX 环境变量 AI_WEB_ENGINE_API_KEY，页面不会保存密钥";
  }
}

function showDialog(dialog) {
  if (typeof dialog.showModal === "function") dialog.showModal();
  else dialog.setAttribute("open", "");
}

function closeDialog(dialog) {
  if (typeof dialog.close === "function") dialog.close();
  else dialog.removeAttribute("open");
}

async function openSettingsDialog() {
  try {
    const config = await api("/api/config").then((response) => response.json());
    setConfigForm(config);
    showDialog($("configDialog"));
  } catch (error) {
    showNotice(`读取模型设置失败：${error.message}`, "error");
  }
}

async function saveConfig(event) {
  event.preventDefault();
  const saveButton = $("saveConfig");
  const models = parseJSONLines(
    $("models").value
      .split("\n")
      .map((id) => id.trim())
      .filter(Boolean)
      .map((id) => JSON.stringify({ id, enabled: true }))
      .join("\n"),
  );
  const defaultModel = $("defaultModel").value.trim();
  if (!defaultModel && models.length === 0) {
    showNotice("至少填写一个默认模型 ID 或模型列表。", "error");
    return;
  }
  setBusy(saveButton, true, "保存中…");
  try {
    const body = {
      provider: $("provider").value.trim(),
      endpoint: $("endpoint").value.trim(),
      protocol: $("protocol").value,
      apiKey: $("apiKey").value,
      defaultModelId: defaultModel,
      models,
      reasoningLevel: $("configReasoning").value,
    };
    await api("/api/config", { method: "POST", body: JSON.stringify(body) });
    closeDialog($("configDialog"));
    showNotice("模型配置已保存，正在刷新连接状态。", "info");
    await refresh();
  } catch (error) {
    showNotice(`保存模型配置失败：${error.message}`, "error");
  } finally {
    setBusy(saveButton, false);
  }
}

async function sendMessage(event) {
  event.preventDefault();
  if (state.sending) return;
  if (!state.configured) {
    showNotice("请先完成模型配置，再发送消息。", "warn");
    await openSettingsDialog();
    return;
  }
  const message = $("message").value.trim();
  if (!message) return;
  if (!$("model").value) {
    showNotice("没有可用模型，请先在模型设置中填写模型 ID。", "warn");
    await openSettingsDialog();
    return;
  }

  state.sending = true;
  state.controller = new AbortController();
  $("message").value = "";
  setConfigured(true);
  setBusy($("sendButton"), true, "生成中…");
  const userMessage = addMessage("user", message);
  const assistantMessage = addMessage("assistant", "正在生成…");
  let received = false;
  try {
    if (!state.sessionId) {
      const session = await api("/api/sessions", { method: "POST" }).then((response) => response.json());
      state.sessionId = session.id;
    }
    const response = await api("/api/chat", {
      method: "POST",
      signal: state.controller.signal,
      body: JSON.stringify({
        sessionId: state.sessionId,
        message,
        modelId: $("model").value,
        reasoningLevel: $("reasoning").value,
      }),
    });
    if (!response.body) throw new Error("浏览器不支持流式响应");
    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    let buffer = "";
    assistantMessage.textContent = "";
    while (true) {
      const result = await reader.read();
      buffer += decoder.decode(result.value || new Uint8Array(), { stream: !result.done });
      const events = buffer.split("\n\n");
      buffer = events.pop() || "";
      for (const eventText of events) {
        const data = eventText
          .split("\n")
          .filter((line) => line.startsWith("data:"))
          .map((line) => line.slice(5).trim())
          .join("\n");
        if (!data) continue;
        let event;
        try {
          event = JSON.parse(data);
        } catch {
          continue;
        }
        if (event.type === "delta") {
          if (!received) assistantMessage.textContent = "";
          received = true;
          assistantMessage.textContent += event.content || "";
          $("chat").scrollTop = $("chat").scrollHeight;
        } else if (event.type === "error") {
          throw new Error(event.message || "模型服务返回错误");
        } else if (event.type === "done" && event.sessionId) {
          state.sessionId = event.sessionId;
        }
      }
      if (result.done) break;
    }
    if (!received && !assistantMessage.textContent) {
      assistantMessage.textContent = "模型没有返回内容。";
      showNotice("模型服务已响应，但没有返回文本。", "warn");
    } else {
      showNotice("");
    }
    await refresh();
  } catch (error) {
    if (error.name === "AbortError") {
      assistantMessage.textContent = "已取消生成。";
    } else {
      assistantMessage.textContent = `生成失败：${error.message}`;
      assistantMessage.classList.add("error-message");
      showNotice(`模型请求失败：${error.message}`, "error");
    }
    userMessage.scrollIntoView({ block: "nearest" });
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

on("newSession", "click", createSession);
on("settings", "click", openSettingsDialog);
on("configForm", "submit", saveConfig);
on("composer", "submit", sendMessage);
on("cancelConfig", "click", () => closeDialog($("configDialog")));
on("reasoning", "change", () => {
  const reasoning = $("reasoning");
  if (reasoning && reasoning.value) showNotice(`本次请求将使用“${reasoning.selectedOptions[0].textContent}”推理等级。`, "info");
});

clearChat("输入需求开始生成 ShortX 指令。");
refresh({ openSettings: true });
