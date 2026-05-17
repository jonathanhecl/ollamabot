const state = {
  models: [],
  activeModel: localStorage.getItem("ollamabot.mainModel") || "",
  messages: [],
};

const els = {
  modelsBody: document.querySelector("#modelsBody"),
  activeModel: document.querySelector("#activeModel"),
  messages: document.querySelector("#messages"),
  form: document.querySelector("#chatForm"),
  prompt: document.querySelector("#prompt"),
  refresh: document.querySelector("#refreshModels"),
  baseUrl: document.querySelector("#baseUrl"),
  version: document.querySelector("#version"),
  cacheState: document.querySelector("#cacheState"),
  think: document.querySelector("#thinkToggle"),
};

els.refresh.addEventListener("click", loadModels);
els.form.addEventListener("submit", sendMessage);

loadModels();

async function loadModels() {
  els.modelsBody.innerHTML = `<tr><td colspan="5">Loading models...</td></tr>`;
  const response = await fetch("/api/models");
  const data = await response.json();
  if (!response.ok) {
    els.modelsBody.innerHTML = `<tr><td colspan="5">${escapeHtml(data.error || "Failed to load models")}</td></tr>`;
    return;
  }
  state.models = data.models || [];
  if (!state.activeModel) {
    const preferred = state.models.find((m) => m.is_default) || state.models.find((m) => m.capabilities?.completion === "comprobado");
    state.activeModel = preferred?.name || "";
  }
  els.baseUrl.textContent = data.base_url;
  els.version.textContent = `Ollama ${data.ollama_version || "unknown"}`;
  els.cacheState.textContent = data.from_cache ? "cache fallback" : "live";
  renderModels();
  renderActive();
}

function renderModels() {
  els.modelsBody.innerHTML = "";
  for (const model of state.models) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>
        <div class="model-name">${escapeHtml(model.name)}</div>
        <div class="sub">${escapeHtml(model.family || "-")} · ${escapeHtml(model.parameters || "-")} · ${escapeHtml(model.quantization || "-")}</div>
      </td>
      <td><div class="caps">${capBadges(model.capabilities)}</div></td>
      <td class="memory">
        <strong>${model.loaded ? formatBytes(model.size_vram) : "not loaded"}</strong>
        <span class="sub">${model.loaded ? `file ${formatBytes(model.size)}` : "VRAM unavailable until loaded"}</span>
      </td>
      <td>${model.context_length || "-"}<div class="sub">${model.loaded ? "loaded" : "available"}</div></td>
      <td><button class="choose ${model.name === state.activeModel ? "active" : ""}" data-model="${escapeAttr(model.name)}">Main</button></td>
    `;
    els.modelsBody.appendChild(tr);
  }
  document.querySelectorAll(".choose").forEach((button) => {
    button.addEventListener("click", () => {
      state.activeModel = button.dataset.model;
      localStorage.setItem("ollamabot.mainModel", state.activeModel);
      renderModels();
      renderActive();
    });
  });
}

function renderActive() {
  els.activeModel.textContent = state.activeModel || "Select a model";
}

function renderMessages() {
  els.messages.innerHTML = "";
  for (const message of state.messages) {
    const div = document.createElement("div");
    div.className = `message ${message.role}`;
    div.innerHTML = `<span class="role">${escapeHtml(message.role)}</span>${escapeHtml(message.content || message.thinking || "")}`;
    els.messages.appendChild(div);
  }
  els.messages.scrollTop = els.messages.scrollHeight;
}

async function sendMessage(event) {
  event.preventDefault();
  const content = els.prompt.value.trim();
  if (!content || !state.activeModel) return;
  els.prompt.value = "";
  state.messages.push({ role: "user", content });
  renderMessages();

  const response = await fetch("/api/chat", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      model: state.activeModel,
      messages: state.messages,
      think: els.think.checked,
    }),
  });
  const data = await response.json();
  if (!response.ok) {
    state.messages.push({ role: "assistant", content: `Error: ${data.error || "request failed"}` });
  } else {
    state.messages.push({ role: "assistant", content: data.message.content || "", thinking: data.message.thinking || "" });
  }
  renderMessages();
  loadModels();
}

function capBadges(caps = {}) {
  const order = ["completion", "tools", "thinking", "vision", "embedding", "audio", "video"];
  return order.map((name) => {
    const status = caps[name] || "pendiente";
    const cls = status === "comprobado" ? "ok" : status === "inferido" ? "inferred" : "";
    return `<span class="cap ${cls}">${name}</span>`;
  }).join("");
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
