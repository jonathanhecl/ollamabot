const state = {
  models: [],
  activeModel: localStorage.getItem("ollamabot.mainModel") || "",
  messages: [],
  attachments: [],
  settings: {},
};

const els = {
  activeModel: document.querySelector("#activeModel"),
  messages: document.querySelector("#messages"),
  form: document.querySelector("#chatForm"),
  prompt: document.querySelector("#prompt"),
  baseUrl: document.querySelector("#baseUrl"),
  version: document.querySelector("#version"),
  cacheState: document.querySelector("#cacheState"),
  memoryState: document.querySelector("#memoryState"),
  think: document.querySelector("#thinkToggle"),
  thinkControl: document.querySelector("#thinkControl"),
  imageControl: document.querySelector("#imageControl"),
  audioControl: document.querySelector("#audioControl"),
  imageInput: document.querySelector("#imageInput"),
  audioInput: document.querySelector("#audioInput"),
  capabilityBar: document.querySelector("#capabilityBar"),
  attachments: document.querySelector("#attachments"),
  modelsDialog: document.querySelector("#modelsDialog"),
  settingsDialog: document.querySelector("#settingsDialog"),
  modelsBody: document.querySelector("#modelsBody"),
  openModels: document.querySelector("#openModels"),
  openSettings: document.querySelector("#openSettings"),
  settingsForm: document.querySelector("#settingsForm"),
  ollamaUrl: document.querySelector("#ollamaUrl"),
};

els.openModels.addEventListener("click", () => {
  renderModels();
  els.modelsDialog.showModal();
});
els.openSettings.addEventListener("click", () => {
  els.ollamaUrl.value = state.settings.ollama_base_url || "";
  els.settingsDialog.showModal();
});
els.settingsForm.addEventListener("submit", saveSettings);
els.form.addEventListener("submit", sendMessage);
els.imageInput.addEventListener("change", () => addFiles([...els.imageInput.files], "image"));
els.audioInput.addEventListener("change", () => addFiles([...els.audioInput.files], "audio"));
document.addEventListener("paste", handlePaste);

bootstrap();

async function bootstrap() {
  await loadSettings();
  await loadModels();
}

async function loadSettings() {
  const response = await fetch("/api/settings");
  if (!response.ok) return;
  state.settings = await response.json();
  els.ollamaUrl.value = state.settings.ollama_base_url || "";
}

async function saveSettings(event) {
  event.preventDefault();
  const response = await fetch("/api/settings", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ollama_base_url: els.ollamaUrl.value.trim() }),
  });
  const data = await response.json();
  if (!response.ok) {
    addSystemMessage(`Settings error: ${data.error || "could not save settings"}`);
    return;
  }
  state.settings = data;
  els.settingsDialog.close();
  await loadModels();
}

async function loadModels() {
  els.modelsBody.innerHTML = `<div class="empty">Loading models...</div>`;
  const response = await fetch("/api/models");
  const data = await response.json();
  if (!response.ok) {
    els.modelsBody.innerHTML = `<div class="empty">${escapeHtml(data.error || "Failed to load models")}</div>`;
    return;
  }
  state.models = data.models || [];
  if (!state.activeModel || !state.models.some((m) => m.name === state.activeModel)) {
    const preferred = state.models.find((m) => m.is_default) || state.models.find((m) => m.capabilities?.completion === "comprobado");
    state.activeModel = preferred?.name || "";
  }
  els.baseUrl.textContent = data.base_url;
  els.version.textContent = `Ollama ${data.ollama_version || "unknown"}`;
  els.cacheState.textContent = data.from_cache ? "cache fallback" : "live";
  const loaded = state.models.filter((m) => m.loaded);
  const vram = loaded.reduce((sum, model) => sum + (model.size_vram || 0), 0);
  els.memoryState.textContent = `${loaded.length} loaded · ${formatBytes(vram)}`;
  renderActive();
  renderModels();
}

function activeModel() {
  return state.models.find((model) => model.name === state.activeModel);
}

function renderActive() {
  const model = activeModel();
  els.activeModel.textContent = state.activeModel || "Select a model";
  const caps = model?.capabilities || {};
  els.capabilityBar.innerHTML = capBadges(caps);
  setCapabilityVisibility();
}

function setCapabilityVisibility() {
  const caps = activeModel()?.capabilities || {};
  const canThink = caps.thinking === "comprobado";
  const canImage = caps.vision === "comprobado" || caps.vision === "inferido";
  const canAudio = caps.audio === "comprobado" || caps.audio === "inferido";
  els.thinkControl.hidden = !canThink;
  els.imageControl.hidden = !canImage;
  els.audioControl.hidden = !canAudio;
  if (!canThink) els.think.checked = false;
}

function renderModels() {
  els.modelsBody.innerHTML = "";
  for (const model of state.models) {
    const card = document.createElement("article");
    card.className = `model-card ${model.name === state.activeModel ? "selected" : ""}`;
    card.innerHTML = `
      <div>
        <div class="model-name">${escapeHtml(model.name)}</div>
        <div class="sub">${escapeHtml(model.family || "-")} · ${escapeHtml(model.parameters || "-")} · ${escapeHtml(model.quantization || "-")}</div>
      </div>
      <div class="caps">${capBadges(model.capabilities)}</div>
      <div class="model-meta">
        <span>${model.loaded ? "loaded" : "available"}</span>
        <span>${model.loaded ? formatBytes(model.size_vram) : "not in memory"}</span>
        <span>ctx ${model.context_length || "-"}</span>
      </div>
      <button class="choose ${model.name === state.activeModel ? "active" : ""}" data-model="${escapeAttr(model.name)}">Main</button>
    `;
    els.modelsBody.appendChild(card);
  }
  document.querySelectorAll(".choose").forEach((button) => {
    button.addEventListener("click", () => {
      state.activeModel = button.dataset.model;
      localStorage.setItem("ollamabot.mainModel", state.activeModel);
      renderActive();
      renderModels();
      els.modelsDialog.close();
    });
  });
}

async function addFiles(files, expectedKind = "") {
  for (const file of files) {
    const kind = file.type.startsWith("audio/") ? "audio" : file.type.startsWith("image/") ? "image" : expectedKind;
    if (!kind) continue;
    const base64 = await fileToBase64(file);
    state.attachments.push({ name: file.name, mime: file.type || `${kind}/*`, kind, data: base64 });
  }
  els.imageInput.value = "";
  els.audioInput.value = "";
  renderAttachments();
}

function handlePaste(event) {
  const files = [];
  for (const item of event.clipboardData?.items || []) {
    if (item.kind === "file") {
      const file = item.getAsFile();
      if (file) files.push(file);
    }
  }
  if (files.length > 0) {
    event.preventDefault();
    addFiles(files);
  }
}

function renderAttachments() {
  els.attachments.innerHTML = "";
  for (const [index, attachment] of state.attachments.entries()) {
    const chip = document.createElement("button");
    chip.type = "button";
    chip.className = `attachment ${attachment.kind}`;
    chip.textContent = `${attachment.kind}: ${attachment.name || "pasted file"}`;
    chip.title = "Remove attachment";
    chip.addEventListener("click", () => {
      state.attachments.splice(index, 1);
      renderAttachments();
    });
    els.attachments.appendChild(chip);
  }
}

async function sendMessage(event) {
  event.preventDefault();
  const content = els.prompt.value.trim();
  if ((!content && state.attachments.length === 0) || !state.activeModel) return;
  const images = state.attachments.map((attachment) => attachment.data);
  const userMessage = { role: "user", content, images };
  state.messages.push(userMessage);
  const visibleAttachments = [...state.attachments];
  state.attachments = [];
  els.prompt.value = "";
  renderAttachments();
  renderMessages();
  if (visibleAttachments.length) {
    addSystemMessage(`Attached ${visibleAttachments.map((item) => item.kind).join(", ")} using Ollama multimodal payload.`);
  }

  const outboundMessages = state.messages.filter((msg) => msg.role === "user" || msg.role === "assistant").map((msg) => ({
    role: msg.role,
    content: msg.content || "",
    images: msg.images || undefined,
  }));
  const assistant = { role: "assistant", content: "", thinking: "", toolCalls: [] };
  state.messages.push(assistant);
  renderMessages();

  const response = await fetch("/api/chat/stream", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      model: state.activeModel,
      messages: outboundMessages,
      think: els.think.checked,
    }),
  });
  if (!response.ok || !response.body) {
    assistant.content = `Error: ${response.statusText}`;
    renderMessages();
    return;
  }
  await readEventStream(response.body, {
    thinking: (value) => {
      assistant.thinking += value;
      renderMessages();
    },
    content: (value) => {
      assistant.content += value;
      renderMessages();
    },
    tool_call: (value) => {
      assistant.toolCalls.push(value);
      renderMessages();
    },
    error: (value) => {
      assistant.content += `\nError: ${value}`;
      renderMessages();
    },
    done: () => loadModels(),
  });
}

async function readEventStream(stream, handlers) {
  const reader = stream.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    buffer += decoder.decode(value, { stream: true });
    const parts = buffer.split("\n\n");
    buffer = parts.pop() || "";
    for (const part of parts) {
      let event = "message";
      let data = "";
      for (const line of part.split("\n")) {
        if (line.startsWith("event: ")) event = line.slice(7);
        if (line.startsWith("data: ")) data += line.slice(6);
      }
      const parsed = data ? JSON.parse(data) : "";
      handlers[event]?.(parsed);
    }
  }
}

function renderMessages() {
  els.messages.innerHTML = "";
  for (const message of state.messages) {
    if (message.role === "system") continue;
    const div = document.createElement("article");
    div.className = `message ${message.role}`;
    const thinking = message.thinking ? `<details class="thinking" open><summary>thinking</summary><pre>${escapeHtml(message.thinking)}</pre></details>` : "";
    const tools = message.toolCalls?.length ? `<div class="tool-calls">${message.toolCalls.map(renderToolCall).join("")}</div>` : "";
    div.innerHTML = `<span class="role">${escapeHtml(message.role)}</span>${thinking}<div class="markdown">${renderMarkdown(message.content || "")}</div>${tools}`;
    els.messages.appendChild(div);
  }
  els.messages.scrollTop = els.messages.scrollHeight;
}

function renderToolCall(call) {
  const fn = call.function || {};
  return `<details open><summary>tool: ${escapeHtml(fn.name || "unknown")}</summary><pre>${escapeHtml(JSON.stringify(fn.arguments || {}, null, 2))}</pre></details>`;
}

function addSystemMessage(content) {
  state.messages.push({ role: "system", content });
}

function capBadges(caps = {}) {
  const order = ["completion", "tools", "thinking", "vision", "embedding", "audio", "video"];
  return order.map((name) => {
    const status = caps[name] || "pendiente";
    const cls = status === "comprobado" ? "ok" : status === "inferido" ? "inferred" : "";
    return `<span class="cap ${cls}" title="${status}">${name}</span>`;
  }).join("");
}

function renderMarkdown(text) {
  const escaped = escapeHtml(text);
  const lines = escaped.split("\n");
  let inCode = false;
  const html = [];
  for (const line of lines) {
    if (line.startsWith("```")) {
      html.push(inCode ? "</code></pre>" : "<pre><code>");
      inCode = !inCode;
      continue;
    }
    if (inCode) {
      html.push(`${line}\n`);
      continue;
    }
    if (line.startsWith("### ")) html.push(`<h3>${inlineMd(line.slice(4))}</h3>`);
    else if (line.startsWith("## ")) html.push(`<h2>${inlineMd(line.slice(3))}</h2>`);
    else if (line.startsWith("# ")) html.push(`<h1>${inlineMd(line.slice(2))}</h1>`);
    else if (line.startsWith("- ")) html.push(`<p class="li">• ${inlineMd(line.slice(2))}</p>`);
    else if (line.trim() === "") html.push("<br>");
    else html.push(`<p>${inlineMd(line)}</p>`);
  }
  if (inCode) html.push("</code></pre>");
  return html.join("");
}

function inlineMd(text) {
  return text
    .replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>")
    .replace(/`(.+?)`/g, "<code>$1</code>");
}

function fileToBase64(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result).split(",")[1] || "");
    reader.onerror = reject;
    reader.readAsDataURL(file);
  });
}

function formatBytes(bytes) {
  if (!bytes) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${value.toFixed(unit === 0 ? 0 : 1)} ${units[unit]}`;
}

function escapeHtml(value) {
  return String(value).replace(/[&<>"']/g, (char) => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    '"': "&quot;",
    "'": "&#39;",
  }[char]));
}

function escapeAttr(value) {
  return escapeHtml(value).replace(/`/g, "&#96;");
}
