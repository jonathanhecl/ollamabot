const state = {
  models: [],
  activeModel: localStorage.getItem("ollamabot.mainModel") || "",
  visionModel: localStorage.getItem("ollamabot.visionModel") || "",
  audioModel: localStorage.getItem("ollamabot.audioModel") || "",
  embeddingsModel: localStorage.getItem("ollamabot.embeddingsModel") || "",
  messages: [],
  attachments: [],
  settings: {},
  sessions: [],
  activeSessionId: localStorage.getItem("ollamabot.activeSessionId") || null,
  sidebarCollapsed: (() => {
    const saved = localStorage.getItem("ollamabot.sidebarCollapsed");
    return saved === null ? true : saved === "true";
  })(),
  audioContext: null,
  audioSource: null,
  audioProcessor: null,
  audioBuffers: [],
  audioSampleRate: 0,
  isRecording: false,
  selectedMicId: localStorage.getItem("ollamabot.selectedMicId") || "",
  audioStream: null,
  modelSearchQuery: "",
  modelActiveFilter: "all",
  sessionIdToDelete: null,
};

const els = {
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
  capabilityBadges: document.querySelector("#capabilityBadges"),
  attachments: document.querySelector("#attachments"),
  modelsDialog: document.querySelector("#modelsDialog"),
  settingsDialog: document.querySelector("#settingsDialog"),
  imageDialog: document.querySelector("#imageDialog"),
  imageDialogImg: document.querySelector("#imageDialogImg"),
  modelsBody: document.querySelector("#modelsBody"),
  openModels: document.querySelector("#openModels"),
  openSettings: document.querySelector("#openSettings"),
  settingsForm: document.querySelector("#settingsForm"),
  ollamaUrl: document.querySelector("#ollamaUrl"),
  workspacePath: document.querySelector("#workspacePath"),
  sessionsPath: document.querySelector("#sessionsPath"),
  memoryPath: document.querySelector("#memoryPath"),
  webPort: document.querySelector("#webPort"),
  webSearchSelect: document.querySelector("#webSearchSelect"),
  webExposeToggle: document.querySelector("#webExposeToggle"),
  webAutoNameToggle: document.querySelector("#webAutoNameToggle"),
  recordControl: document.querySelector("#recordControl"),
  micSelect: document.querySelector("#micSelect"),
  sidebar: document.querySelector("#sidebar"),
  sessionList: document.querySelector("#sessionList"),
  newSessionBtn: document.querySelector("#newSessionBtn"),
  toggleSidebar: document.querySelector("#toggleSidebar"),
  contextFill: document.querySelector("#contextFill"),
  contextLabel: document.querySelector("#contextLabel"),
  contextBar: document.querySelector("#contextBar"),
  appLayout: document.querySelector(".app-layout"),
  confirmDialog: document.querySelector("#confirmDialog"),
  cancelConfirmBtn: document.querySelector("#cancelConfirmBtn"),
  okConfirmBtn: document.querySelector("#okConfirmBtn"),
};

els.openModels.addEventListener("click", () => {
  state.modelSearchQuery = "";
  state.modelActiveFilter = "all";
  const searchInput = document.querySelector("#modelSearch");
  if (searchInput) searchInput.value = "";
  document.querySelectorAll(".filter-btn").forEach((btn) => {
    if (btn.dataset.filter === "all") btn.classList.add("active");
    else btn.classList.remove("active");
  });
  renderModels();
  els.modelsDialog.showModal();
});
els.openSettings.addEventListener("click", async () => {
  els.ollamaUrl.value = state.settings.ollama_base_url || "";
  els.workspacePath.value = state.settings.workspace || "";
  els.sessionsPath.value = state.settings.sessions_path || "";
  els.memoryPath.value = state.settings.memory_path || "";
  els.webExposeToggle.checked = !!state.settings.web_expose_network;
  els.webAutoNameToggle.checked = state.settings.web_auto_name !== false;
  els.webSearchSelect.value = state.settings.web_search_enabled ? "ddg" : "none";
  const addr = state.settings.web_addr || ":8080";
  const portMatch = addr.match(/:(\d+)$/);
  els.webPort.value = portMatch ? portMatch[1] : "8080";
  els.settingsDialog.showModal();
  // Request temporary microphone access to prompt permission dialog, so enumerateDevices gets actual labels
  try {
    const tempStream = await navigator.mediaDevices.getUserMedia({ audio: true });
    tempStream.getTracks().forEach(track => track.stop());
  } catch (e) {
    console.warn("Could not prompt mic permission on settings open:", e);
  }
  await populateMicrophones();
});
els.settingsForm.addEventListener("submit", saveSettings);
els.form.addEventListener("submit", sendMessage);
els.imageInput.addEventListener("change", () => addFiles([...els.imageInput.files], "image"));
els.audioInput.addEventListener("change", () => addFiles([...els.audioInput.files], "audio"));
els.recordControl.addEventListener("click", toggleRecording);
document.addEventListener("paste", handlePaste);

// Models dialog filtering wiring
const searchInput = document.querySelector("#modelSearch");
if (searchInput) {
  searchInput.addEventListener("input", (e) => {
    state.modelSearchQuery = e.target.value;
    renderModels();
  });
}

document.querySelectorAll(".filter-btn").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll(".filter-btn").forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    state.modelActiveFilter = btn.dataset.filter;
    renderModels();
  });
});

els.prompt.addEventListener("keydown", (e) => {
  if (e.key === "Enter" && !e.shiftKey) {
    e.preventDefault();
    els.form.dispatchEvent(new Event("submit", { cancelable: true }));
  }
});

document.addEventListener("click", (e) => {
  const preview = e.target.closest(".media-preview.image");
  if (!preview) return;
  const url = preview.dataset.url;
  if (!url) return;
  els.imageDialogImg.src = url;
  els.imageDialog.showModal();
});

// Close image lightbox wiring
const closeBtn = document.getElementById("imageDialogClose");
if (closeBtn) {
  closeBtn.addEventListener("click", (e) => {
    e.stopPropagation();
    els.imageDialog.close();
  });
}

// Confirm dialog wiring
if (els.cancelConfirmBtn) {
  els.cancelConfirmBtn.addEventListener("click", () => {
    els.confirmDialog.close();
    state.sessionIdToDelete = null;
  });
}

if (els.okConfirmBtn) {
  els.okConfirmBtn.addEventListener("click", () => {
    if (state.sessionIdToDelete) {
      deleteSession(state.sessionIdToDelete);
      state.sessionIdToDelete = null;
    }
    els.confirmDialog.close();
  });
}

// Close on Escape key
els.imageDialog.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    els.imageDialog.close();
  }
});

// Close image lightbox on backdrop click (click outside dialog content)
els.imageDialog.addEventListener("click", (e) => {
  const rect = els.imageDialog.getBoundingClientRect();
  const isInDialog = (
    rect.top <= e.clientY &&
    e.clientY <= rect.top + rect.height &&
    rect.left <= e.clientX &&
    e.clientX <= rect.left + rect.width
  );
  if (!isInDialog) {
    els.imageDialog.close();
  }
});

const dropZone = document.querySelector(".app");
dropZone.addEventListener("dragover", (e) => {
  const hasFiles = [...(e.dataTransfer?.items || [])].some((i) => i.kind === "file");
  if (!hasFiles) return;
  e.preventDefault();
  dropZone.classList.add("drag-over");
});
dropZone.addEventListener("dragleave", (e) => {
  if (!dropZone.contains(e.relatedTarget)) dropZone.classList.remove("drag-over");
});
dropZone.addEventListener("drop", (e) => {
  e.preventDefault();
  dropZone.classList.remove("drag-over");
  const files = [...(e.dataTransfer?.files || [])].filter((f) => {
    const kind = f.type.startsWith("audio/") ? "audio" : f.type.startsWith("image/") ? "image" : "";
    return kind && capabilityFor(kind);
  });
  if (files.length > 0) addFiles(files);
});

els.newSessionBtn.addEventListener("click", () => createSession());
els.toggleSidebar.addEventListener("click", () => {
  state.sidebarCollapsed = !state.sidebarCollapsed;
  localStorage.setItem("ollamabot.sidebarCollapsed", state.sidebarCollapsed ? "true" : "false");
  applySidebarState();
  els.toggleSidebar.textContent = state.sidebarCollapsed ? "❱" : "❰";
});
els.sessionList.addEventListener("click", (e) => {
  const deleteBtn = e.target.closest(".session-delete");
  if (deleteBtn) {
    const item = deleteBtn.closest(".session-item");
    if (!item) return;
    const id = item.dataset.id;
    if (id) {
      state.sessionIdToDelete = id;
      els.confirmDialog.showModal();
    }
    return;
  }

  const renameBtn = e.target.closest(".session-rename-btn");
  if (renameBtn) {
    const item = renameBtn.closest(".session-item");
    if (!item) return;
    const id = item.dataset.id;
    const titleSpan = item.querySelector(".session-title");
    const titleRow = item.querySelector(".session-title-row");
    if (!id || !titleSpan || !titleRow) return;

    e.stopPropagation();

    if (titleRow.querySelector(".session-title-input")) return;

    const input = document.createElement("input");
    input.type = "text";
    input.className = "session-title-input";
    input.value = titleSpan.textContent;

    titleSpan.style.display = "none";
    renameBtn.style.display = "none";
    titleRow.appendChild(input);
    input.focus();
    input.select();

    const saveRename = async () => {
      const newTitle = input.value.trim();
      if (newTitle && newTitle !== titleSpan.textContent) {
        titleSpan.textContent = newTitle;
        try {
          await fetch(`/api/sessions/${encodeURIComponent(id)}`, {
            method: "PUT",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ title: newTitle }),
          });
          const session = state.sessions.find(s => s.id === id);
          if (session) session.title = newTitle;
          renderSessions();
        } catch (err) {
          console.error("Rename failed:", err);
        }
      } else {
        titleSpan.style.display = "";
        renameBtn.style.display = "";
        input.remove();
      }
    };

    input.addEventListener("keydown", (evt) => {
      if (evt.key === "Enter") {
        evt.preventDefault();
        saveRename();
      } else if (evt.key === "Escape") {
        evt.preventDefault();
        titleSpan.style.display = "";
        renameBtn.style.display = "";
        input.remove();
      }
    });

    input.addEventListener("blur", () => {
      saveRename();
    });

    return;
  }

  const item = e.target.closest(".session-item");
  if (!item) return;
  if (e.target.closest(".session-title-input")) return;
  const id = item.dataset.id;
  if (id) loadSession(id);
});

els.sessionList.addEventListener("dblclick", (e) => {
  const item = e.target.closest(".session-item");
  if (!item) return;
  const renameBtn = item.querySelector(".session-rename-btn");
  if (renameBtn) {
    renameBtn.click();
  }
});

bootstrap();

async function bootstrap() {
  await loadSettings();
  await loadModels();
  applySidebarState();
  requestAnimationFrame(() => document.body.classList.remove("first-load"));
  await loadSessions();
  if (state.activeSessionId) {
    await loadSession(state.activeSessionId);
  } else {
    await createSession();
  }
}

function applySidebarState() {
  if (state.sidebarCollapsed) {
    els.appLayout.classList.add("sidebar-collapsed");
  } else {
    els.appLayout.classList.remove("sidebar-collapsed");
  }
  if (els.toggleSidebar) {
    els.toggleSidebar.textContent = state.sidebarCollapsed ? "❱" : "❰";
  }
}

async function loadSettings() {
  const response = await fetch("/api/settings");
  if (!response.ok) return;
  state.settings = await response.json();
  els.ollamaUrl.value = state.settings.ollama_base_url || "";
  els.workspacePath.value = state.settings.workspace || "";
  els.sessionsPath.value = state.settings.sessions_path || "";
  els.memoryPath.value = state.settings.memory_path || "";
  els.webExposeToggle.checked = !!state.settings.web_expose_network;
  els.webAutoNameToggle.checked = state.settings.web_auto_name !== false;
  els.webSearchSelect.value = state.settings.web_search_enabled ? "ddg" : "none";
  const waddr = state.settings.web_addr || ":8080";
  const wportMatch = waddr.match(/:(\d+)$/);
  els.webPort.value = wportMatch ? wportMatch[1] : "8080";
  if (state.settings.model_vision) state.visionModel = state.settings.model_vision;
  if (state.settings.model_audio) state.audioModel = state.settings.model_audio;
  if (state.settings.model_embeddings) state.embeddingsModel = state.settings.model_embeddings;
  localStorage.setItem("ollamabot.visionModel", state.visionModel);
  localStorage.setItem("ollamabot.audioModel", state.audioModel);
  localStorage.setItem("ollamabot.embeddingsModel", state.embeddingsModel);
}

async function saveSettings(event) {
  event.preventDefault();
  state.selectedMicId = els.micSelect.value;
  localStorage.setItem("ollamabot.selectedMicId", state.selectedMicId);
  const response = await fetch("/api/settings", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      ollama_base_url: els.ollamaUrl.value.trim(),
      workspace: els.workspacePath.value.trim(),
      sessions_path: els.sessionsPath.value.trim(),
      memory_path: els.memoryPath.value.trim(),
      model_vision: state.visionModel,
      model_audio: state.audioModel,
      model_embeddings: state.embeddingsModel,
      web_search_enabled: els.webSearchSelect.value === "ddg",
      web_expose_network: els.webExposeToggle.checked,
      web_auto_name: els.webAutoNameToggle.checked,
      web_addr: ":" + (els.webPort.value.trim().replace(/^:/, "") || "8080"),
    }),
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

async function saveRoleModels() {
  await fetch("/api/settings", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      ollama_base_url: state.settings.ollama_base_url || "",
      workspace: state.settings.workspace || "",
      sessions_path: state.settings.sessions_path || "",
      memory_path: state.settings.memory_path || "",
      model_vision: state.visionModel,
      model_audio: state.audioModel,
      model_embeddings: state.embeddingsModel,
      web_search_enabled: state.settings.web_search_enabled || false,
      web_expose_network: state.settings.web_expose_network || false,
      web_auto_name: state.settings.web_auto_name !== false,
    }),
  });
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
    const preferred = state.models.find((m) => m.is_default && canBeMain(m)) || state.models.find((m) => canBeMain(m));
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
  const modelName = state.activeModel || "Select a model";
  const caps = model?.capabilities || {};
  let html = `<span class="cap model-badge" title="Active model">${escapeHtml(modelName)}</span>`;
  html += capBadges(caps);
  const roleLabels = [
    { key: "visionModel", label: "vision" },
    { key: "audioModel", label: "audio" },
    { key: "embeddingsModel", label: "embed" },
  ];
  for (const { key, label } of roleLabels) {
    const name = state[key];
    if (name && name !== state.activeModel) {
      html += `<span class="cap role-badge" title="${label} model: ${escapeHtml(name)}">${label}: ${escapeHtml(name.split(":")[0])}</span>`;
    }
  }
  els.capabilityBadges.innerHTML = html;
  setCapabilityVisibility();
}

function setCapabilityVisibility() {
  const caps = activeModel()?.capabilities || {};
  const canThink = caps.thinking === "comprobado";
  const canImage = modelForRole("vision") !== null;
  const canAudio = modelForRole("audio") !== null;
  els.thinkControl.hidden = !canThink;
  els.imageControl.hidden = !canImage;
  els.audioControl.hidden = !canAudio;
  els.recordControl.hidden = !canAudio;
  if (!canThink) els.think.checked = false;
  state.attachments = state.attachments.filter((attachment) => capabilityFor(attachment.kind));
  renderAttachments();
}

// Returns true if a model meets the minimum requirements for the main role.
function canBeMain(model) {
  const caps = model?.capabilities || {};
  return caps.completion === "comprobado" && caps.tools === "comprobado";
}

// Returns the model name that handles a given role, or null if unavailable.
// Priority: dedicated role model (if set) → main model (if capable) → null.
function modelForRole(role) {
  const capKey = role === "vision" ? "vision" : "audio";
  const dedicated = role === "vision" ? state.visionModel : state.audioModel;
  if (dedicated) {
    const m = state.models.find((m) => m.name === dedicated);
    const status = m?.capabilities?.[capKey];
    if (status === "comprobado" || (role === "vision" && status === "inferido")) return dedicated;
    return null;
  }
  const main = activeModel();
  if (!main) return null;
  const status = main.capabilities?.[capKey];
  if (status === "comprobado" || (role === "vision" && status === "inferido")) return main.name;
  return null;
}

function renderModels() {
  els.modelsBody.innerHTML = "";
  const query = state.modelSearchQuery.toLowerCase().trim();
  const filter = state.modelActiveFilter;
  let filteredModels = state.models;

  if (query) {
    filteredModels = filteredModels.filter((m) =>
      m.name.toLowerCase().includes(query) ||
      (m.family && m.family.toLowerCase().includes(query)) ||
      (m.parameters && m.parameters.toLowerCase().includes(query))
    );
  }

  if (filter === "loaded") {
    filteredModels = filteredModels.filter((m) => m.loaded);
  } else if (filter === "main") {
    filteredModels = filteredModels.filter((m) => canBeMain(m));
  } else if (filter === "vision") {
    filteredModels = filteredModels.filter((m) => {
      const cap = m.capabilities?.vision;
      return cap === "comprobado" || cap === "inferido";
    });
  } else if (filter === "audio") {
    filteredModels = filteredModels.filter((m) => {
      const cap = m.capabilities?.audio;
      return cap === "comprobado" || cap === "inferido";
    });
  } else if (filter === "embeddings") {
    filteredModels = filteredModels.filter((m) => {
      const cap = m.capabilities?.embedding;
      return cap === "comprobado" || cap === "inferido";
    });
  }

  // Sort models: active first, then useful ones, and useless ones at the very bottom
  filteredModels.sort((a, b) => {
    const aMain = canBeMain(a);
    const aVis = a.capabilities?.vision === "comprobado" || a.capabilities?.vision === "inferido";
    const aAud = a.capabilities?.audio === "comprobado" || a.capabilities?.audio === "inferido";
    const aEmb = a.capabilities?.embedding === "comprobado" || a.capabilities?.embedding === "inferido";
    const aUseless = !aMain && !aVis && !aAud && !aEmb;

    const bMain = canBeMain(b);
    const bVis = b.capabilities?.vision === "comprobado" || b.capabilities?.vision === "inferido";
    const bAud = b.capabilities?.audio === "comprobado" || b.capabilities?.audio === "inferido";
    const bEmb = b.capabilities?.embedding === "comprobado" || b.capabilities?.embedding === "inferido";
    const bUseless = !bMain && !bVis && !bAud && !bEmb;

    if (aUseless !== bUseless) {
      return aUseless ? 1 : -1;
    }
    const aSelected = a.name === state.activeModel;
    const bSelected = b.name === state.activeModel;
    if (aSelected !== bSelected) {
      return aSelected ? -1 : 1;
    }
    return a.name.localeCompare(b.name);
  });

  if (filteredModels.length === 0) {
    els.modelsBody.innerHTML = `<div class="empty">No models match the filter or search query.</div>`;
    return;
  }

  for (const model of filteredModels) {
    const isMain = model.name === state.activeModel;
    const isVision = model.name === state.visionModel;
    const isAudio = model.name === state.audioModel;
    const isEmbed = model.name === state.embeddingsModel;

    const isMainCapable = canBeMain(model);
    const canVision = model.capabilities?.vision === "comprobado" || model.capabilities?.vision === "inferido";
    const canAudio = model.capabilities?.audio === "comprobado" || model.capabilities?.audio === "inferido";
    const canEmbed = model.capabilities?.embedding === "comprobado" || model.capabilities?.embedding === "inferido";
    const isUseless = !isMainCapable && !canVision && !canAudio && !canEmbed;

    const card = document.createElement("article");
    card.className = `model-card ${isMain ? "selected" : ""} ${isUseless ? "useless" : ""}`;

    const sizeBarPct = model.size ? Math.min(100, Math.round((model.size_vram / model.size) * 100)) : 0;
    const hardwareBarHtml = model.loaded ? `
      <div class="model-hardware-bar" title="Memory usage in VRAM: ${formatBytes(model.size_vram)} / ${formatBytes(model.size)}">
        <div class="hardware-track">
          <div class="hardware-fill active" style="width: ${sizeBarPct}%"></div>
        </div>
        <span>vram ${sizeBarPct}%</span>
      </div>
    ` : `
      <div class="model-hardware-bar" title="Size on disk: ${formatBytes(model.size || model.size_vram)}">
        <div class="hardware-track">
          <div class="hardware-fill" style="width: 0%"></div>
        </div>
        <span>disk ${formatBytes(model.size || model.size_vram)}</span>
      </div>
    `;

    let activeRolesHtml = "";
    if (isMain) activeRolesHtml += `<span class="active-role-pill main" title="This model is assigned to the MAIN role">Main</span>`;
    if (isVision) activeRolesHtml += `<span class="active-role-pill vision" title="This model is assigned to the VISION role">Vision</span>`;
    if (isAudio) activeRolesHtml += `<span class="active-role-pill audio" title="This model is assigned to the AUDIO role">Audio</span>`;
    if (isEmbed) activeRolesHtml += `<span class="active-role-pill embed" title="This model is assigned to the EMBEDDINGS role">Embed</span>`;
    const activeRolesContainer = activeRolesHtml ? `<div class="active-roles-container">${activeRolesHtml}</div>` : "";

    const statusBadgeHtml = model.loaded ?
      `<span class="model-loaded-badge"><span class="pulse-dot"></span>loaded</span>` :
      `<span class="model-offline-badge">offline</span>`;

    let roleButtonsHtml = "";
    if (isMainCapable) {
      roleButtonsHtml += `<button class="choose role-btn ${isMain ? "active" : ""}" data-role="main" data-model="${escapeAttr(model.name)}">⚡ Main</button>`;
    }
    if (canVision) {
      roleButtonsHtml += `<button class="choose role-btn ${isVision ? "active" : ""}" data-role="vision" data-model="${escapeAttr(model.name)}">👁️ Vision</button>`;
    }
    if (canAudio) {
      roleButtonsHtml += `<button class="choose role-btn ${isAudio ? "active" : ""}" data-role="audio" data-model="${escapeAttr(model.name)}">🔊 Audio</button>`;
    }
    if (canEmbed) {
      roleButtonsHtml += `<button class="choose role-btn ${isEmbed ? "active" : ""}" data-role="embeddings" data-model="${escapeAttr(model.name)}">🔗 Embed</button>`;
    }

    const roleButtonsContainer = roleButtonsHtml ? `
      <div class="role-buttons">
        ${roleButtonsHtml}
      </div>
    ` : "";

    card.innerHTML = `
      <div>
        <div class="model-card-header">
          <div class="model-name-wrapper">
            <div class="model-name">${escapeHtml(model.name)}</div>
            ${activeRolesContainer}
          </div>
          ${statusBadgeHtml}
        </div>
        <div class="sub">${escapeHtml(model.family || "-")} · ${escapeHtml(model.parameters || "-")} · ${escapeHtml(model.quantization || "-")}</div>
      </div>
      <div class="caps">${capBadges(model.capabilities)}</div>
      <div class="model-meta">
        <div class="model-meta-info">
          <span>ctx ${model.context_length ? escapeHtml(model.context_length.toLocaleString()) : "-"}</span>
        </div>
        <div style="flex: 1; max-width: 140px;">
          ${hardwareBarHtml}
        </div>
      </div>
      ${roleButtonsContainer}
    `;
    els.modelsBody.appendChild(card);
  }

  document.querySelectorAll(".choose:not([disabled])").forEach((button) => {
    button.addEventListener("click", () => {
      const role = button.dataset.role;
      const model = button.dataset.model;
      if (role === "main") {
        state.activeModel = model;
        localStorage.setItem("ollamabot.mainModel", state.activeModel);
        renderActive();
        renderModels();
        els.modelsDialog.close();
      } else {
        const stateKey = role === "vision" ? "visionModel" : role === "audio" ? "audioModel" : "embeddingsModel";
        const lsKey = role === "vision" ? "ollamabot.visionModel" : role === "audio" ? "ollamabot.audioModel" : "ollamabot.embeddingsModel";
        if (state[stateKey] === model) {
          state[stateKey] = "";
          localStorage.setItem(lsKey, "");
        } else {
          state[stateKey] = model;
          localStorage.setItem(lsKey, model);
        }
        saveRoleModels();
        renderActive();
        renderModels();
      }
    });
  });
}

async function addFiles(files, expectedKind = "") {
  for (const file of files) {
    const kind = file.type.startsWith("audio/") ? "audio" : file.type.startsWith("image/") ? "image" : expectedKind;
    if (!kind) continue;
    if (!capabilityFor(kind)) {
      addSystemMessage(`${kind} is not supported by the selected model.`);
      renderMessages();
      continue;
    }
    const dataURL = await fileToDataURL(file);
    const base64 = dataURL.split(",")[1] || "";
    state.attachments.push({ name: file.name, mime: file.type || `${kind}/*`, kind, data: base64, url: dataURL });
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
    addFiles(files.filter((file) => {
      const kind = file.type.startsWith("audio/") ? "audio" : file.type.startsWith("image/") ? "image" : "";
      return kind && capabilityFor(kind);
    }));
  }
}

function renderAttachments() {
  els.attachments.innerHTML = "";
  for (const [index, attachment] of state.attachments.entries()) {
    const card = document.createElement("article");
    card.className = `attachment ${attachment.kind}`;
    card.innerHTML = `${attachmentPreview(attachment)}<button type="button" title="Remove attachment">Remove</button>`;
    card.querySelector("button").addEventListener("click", () => {
      state.attachments.splice(index, 1);
      renderAttachments();
    });
    els.attachments.appendChild(card);
  }
}

async function sendMessage(event) {
  event.preventDefault();
  const content = els.prompt.value.trim();
  if ((!content && state.attachments.length === 0) || !state.activeModel) return;

  if (!state.activeSessionId) {
    const title = content ? content.slice(0, 40) : "New session";
    await createSession(title);
  }

  state.attachments = state.attachments.filter((attachment) => capabilityFor(attachment.kind));
  const images = state.attachments.map((attachment) => attachment.data);
  const visibleAttachments = [...state.attachments];
  const userMessage = { role: "user", content, images, attachments: visibleAttachments };
  state.messages.push(userMessage);
  state.attachments = [];
  els.prompt.value = "";
  renderAttachments();
  renderMessages();
  updateContextBar();
  if (visibleAttachments.length) {
    addSystemMessage(`Attached ${visibleAttachments.map((item) => item.kind).join(", ")} using Ollama multimodal payload.`);
  }

  const outboundMessages = state.messages.filter((msg) => msg.role === "user" || msg.role === "assistant").map((msg) => ({
    role: msg.role,
    content: msg.content || "",
    images: msg.images || undefined,
    image_kinds: msg.attachments?.map((a) => a.kind) || undefined,
  }));
  const assistant = { role: "assistant", content: "", thinking: "", toolCalls: [], toolResults: [], streaming: true, waiting: true };
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
    assistant.waiting = false;
    assistant.streaming = false;
    renderMessages();
    return;
  }
  await readEventStream(response.body, {
    thinking: (value) => {
      assistant.waiting = false;
      assistant.thinking += value;
      renderMessages();
    },
    content: (value) => {
      assistant.waiting = false;
      assistant.content += value;
      renderMessages();
    },
    tool_call: (value) => {
      assistant.waiting = false;
      assistant.toolCalls.push(value);
      renderMessages();
    },
    tool_start: (value) => {
      assistant.waiting = false;
      assistant.toolResults.push({ name: value.name, arguments: value.arguments, result: null, status: "running" });
      renderMessages();
    },
    tool_result: (value) => {
      assistant.waiting = false;
      const item = assistant.toolResults.find((tr) => tr.name === value.name && tr.status === "running");
      if (item) {
        item.result = value.result;
        item.status = "done";
      } else {
        assistant.toolResults.push({ name: value.name, arguments: null, result: value.result, status: "done" });
      }
      renderMessages();
    },
    error: (value) => {
      assistant.waiting = false;
      assistant.streaming = false;
      assistant.content += `\nError: ${value}`;
      renderMessages();
    },
    done: () => {
      assistant.waiting = false;
      assistant.streaming = false;
      renderMessages();
      updateContextBar();
      saveSession();
      loadModels();

      // Auto-generate session title if enabled and it's the first message exchange
      if (state.settings.web_auto_name !== false) {
        const userMsgs = state.messages.filter((m) => m.role === "user");
        const assistantMsgs = state.messages.filter((m) => m.role === "assistant");
        if (userMsgs.length === 1 && assistantMsgs.length === 1) {
          autoGenerateSessionTitle(assistant.content);
        }
      }
    },
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
    div.className = `message ${message.role} ${message.streaming ? "streaming" : ""}`;
    const thinking = message.thinking ? `<details class="thinking" open><summary>thinking</summary><pre>${escapeHtml(message.thinking)}</pre></details>` : "";
    const tools = message.toolCalls?.length ? `<div class="tool-calls">${message.toolCalls.map(renderToolCall).join("")}</div>` : "";
    const toolResults = message.toolResults?.length ? `<div class="tool-results">${message.toolResults.map(renderToolResult).join("")}</div>` : "";
    const pending = message.waiting ? `<div class="waiting"><span></span><span></span><span></span><em>processing</em></div>` : "";
    const media = message.attachments?.length ? `<div class="message-media">${message.attachments.map(attachmentPreview).join("")}</div>` : "";
    const cursor = message.streaming ? `<span class="stream-cursor"></span>` : "";
    div.innerHTML = `<span class="role">${escapeHtml(message.role)}</span>${media}${pending}${thinking}<div class="markdown">${renderMarkdown(message.content || "")}${cursor}</div>${tools}${toolResults}`;
    els.messages.appendChild(div);
  }
  els.messages.scrollTop = els.messages.scrollHeight;
}

function renderToolCall(call) {
  const fn = call.function || {};
  return `<details open><summary>tool: ${escapeHtml(fn.name || "unknown")}</summary><pre>${escapeHtml(JSON.stringify(fn.arguments || {}, null, 2))}</pre></details>`;
}

function renderToolResult(tr) {
  const status = tr.status === "running" ? "running..." : "done";
  const result = tr.result !== null ? escapeHtml(String(tr.result)) : "";
  return `<details open><summary>tool result: ${escapeHtml(tr.name || "unknown")} (${status})</summary><pre>${result}</pre></details>`;
}

function addSystemMessage(content) {
  state.messages.push({ role: "system", content });
}

function capabilityFor(kind) {
  if (kind === "image") return modelForRole("vision") !== null;
  if (kind === "audio") return modelForRole("audio") !== null;
  return false;
}

function capBadges(caps = {}) {
  const order = ["completion", "tools", "thinking", "vision", "embedding", "audio", "video"];
  const glyphs = {
    completion: "⚡ text",
    tools: "🛠️ tools",
    thinking: "🧠 think",
    vision: "👁️ vision",
    embedding: "🔗 embed",
    audio: "🔊 audio",
    video: "📹 video"
  };
  return order.map((name) => {
    const status = caps ? (caps[name] || "pendiente") : "pendiente";
    const cls = status === "comprobado" ? "ok" : status === "inferido" ? "inferred" : "";
    const label = glyphs[name] || name;
    const engStatus = status === "comprobado" ? "confirmed" : status === "inferido" ? "inferred" : "pending";
    return `<span class="cap ${cls}" title="${name}: ${engStatus}">${label}</span>`;
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

function attachmentPreview(attachment) {
  const label = escapeHtml(attachment.name || attachment.mime || attachment.kind);
  if (attachment.kind === "image") {
    return `<div class="media-preview image" data-url="${escapeAttr(attachment.url)}"><img src="${escapeAttr(attachment.url)}" alt="${label}"><span>${label}</span></div>`;
  }
  if (attachment.kind === "audio") {
    return `<div class="media-preview audio"><span>${label}</span><audio controls src="${escapeAttr(attachment.url)}"></audio></div>`;
  }
  return `<div class="media-preview"><span>${label}</span></div>`;
}

function fileToDataURL(file) {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
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

async function populateMicrophones() {
  if (!navigator.mediaDevices || !navigator.mediaDevices.enumerateDevices) {
    els.micSelect.innerHTML = `<option value="">Microphone API not supported</option>`;
    return;
  }
  try {
    const devices = await navigator.mediaDevices.enumerateDevices();
    const audioMics = devices.filter((d) => d.kind === "audioinput");
    els.micSelect.innerHTML = `<option value="">Default system microphone</option>`;
    for (const mic of audioMics) {
      const option = document.createElement("option");
      option.value = mic.deviceId;
      option.textContent = mic.label || `Microphone (${mic.deviceId.slice(0, 5)})`;
      if (mic.deviceId === state.selectedMicId) {
        option.selected = true;
      }
      els.micSelect.appendChild(option);
    }
  } catch (err) {
    console.warn("enumerateDevices failed:", err);
  }
}

async function toggleRecording() {
  if (state.isRecording) {
    stopRecording();
  } else {
    await startRecording();
  }
}

async function startRecording() {
  if (!navigator.mediaDevices || !navigator.mediaDevices.getUserMedia) {
    addSystemMessage("Audio recording is not supported on this browser.");
    renderMessages();
    return;
  }
  const constraints = {
    audio: state.selectedMicId ? { deviceId: { exact: state.selectedMicId } } : true,
  };
  try {
    state.audioStream = await navigator.mediaDevices.getUserMedia(constraints);
    state.audioContext = new AudioContext();
    state.audioSampleRate = state.audioContext.sampleRate;
    state.audioBuffers = [];
    state.audioSource = state.audioContext.createMediaStreamSource(state.audioStream);
    state.audioProcessor = state.audioContext.createScriptProcessor(4096, 1, 1);
    state.audioProcessor.onaudioprocess = (event) => {
      state.audioBuffers.push(new Float32Array(event.inputBuffer.getChannelData(0)));
    };
    state.audioSource.connect(state.audioProcessor);
    state.audioProcessor.connect(state.audioContext.destination);
    state.isRecording = true;
    updateRecordUI();
  } catch (err) {
    addSystemMessage(`Could not start voice recording: ${err.message}`);
    renderMessages();
  }
}

async function stopRecording() {
  state.isRecording = false;
  updateRecordUI();
  if (state.audioProcessor) {
    state.audioProcessor.disconnect();
    state.audioProcessor.onaudioprocess = null;
    state.audioProcessor = null;
  }
  if (state.audioSource) {
    state.audioSource.disconnect();
    state.audioSource = null;
  }
  if (state.audioContext) {
    await state.audioContext.close();
    state.audioContext = null;
  }
  if (state.audioStream) {
    state.audioStream.getTracks().forEach((t) => t.stop());
    state.audioStream = null;
  }
  const blob = createWavBlob(state.audioBuffers, state.audioSampleRate);
  state.audioBuffers = [];
  state.audioSampleRate = 0;
  if (blob.size === 0) {
    return;
  }
  const file = new File([blob], `mic_record_${Date.now()}.wav`, { type: "audio/wav" });
  await addFiles([file], "audio");
}

function updateRecordUI() {
  if (state.isRecording) {
    els.recordControl.classList.add("active");
    els.recordControl.querySelector(".record-label").textContent = "Recording...";
    els.recordControl.querySelector(".record-icon").textContent = "🔴";
  } else {
    els.recordControl.classList.remove("active");
    els.recordControl.querySelector(".record-label").textContent = "Record";
    els.recordControl.querySelector(".record-icon").textContent = "🎤";
  }
}

function createWavBlob(buffers, sampleRate) {
  if (!buffers.length || !sampleRate) return new Blob([], { type: "audio/wav" });
  const samples = mergeAudioBuffers(buffers);
  const buffer = new ArrayBuffer(44 + samples.length * 2);
  const view = new DataView(buffer);
  writeAscii(view, 0, "RIFF");
  view.setUint32(4, 36 + samples.length * 2, true);
  writeAscii(view, 8, "WAVE");
  writeAscii(view, 12, "fmt ");
  view.setUint32(16, 16, true);
  view.setUint16(20, 1, true);
  view.setUint16(22, 1, true);
  view.setUint32(24, sampleRate, true);
  view.setUint32(28, sampleRate * 2, true);
  view.setUint16(32, 2, true);
  view.setUint16(34, 16, true);
  writeAscii(view, 36, "data");
  view.setUint32(40, samples.length * 2, true);
  writePcm16(view, 44, samples);
  return new Blob([view], { type: "audio/wav" });
}

function mergeAudioBuffers(buffers) {
  const length = buffers.reduce((sum, buffer) => sum + buffer.length, 0);
  const merged = new Float32Array(length);
  let offset = 0;
  for (const buffer of buffers) {
    merged.set(buffer, offset);
    offset += buffer.length;
  }
  return merged;
}

function writePcm16(view, offset, samples) {
  for (let i = 0; i < samples.length; i++) {
    const sample = Math.max(-1, Math.min(1, samples[i]));
    view.setInt16(offset + i * 2, sample < 0 ? sample * 0x8000 : sample * 0x7fff, true);
  }
}

function writeAscii(view, offset, value) {
  for (let i = 0; i < value.length; i++) {
    view.setUint8(offset + i, value.charCodeAt(i));
  }
}

// ----- Sessions -----

async function loadSessions() {
  try {
    const response = await fetch("/api/sessions");
    if (!response.ok) return;
    state.sessions = await response.json();
    renderSessions();
  } catch (e) {
    console.warn("loadSessions failed:", e);
  }
}

async function createSession(title = "New session") {
  try {
    const response = await fetch("/api/sessions", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ title, model: state.activeModel }),
    });
    if (!response.ok) return;
    const sess = await response.json();
    state.activeSessionId = sess.id;
    localStorage.setItem("ollamabot.activeSessionId", sess.id);
    state.messages = [];
    state.attachments = [];
    return order.map((name) => {
      const status = caps[name] || "pendiente";
      const cls = status === "comprobado" ? "ok" : status === "inferido" ? "inferred" : "";
      const label = glyphs[name] || name;
      const engStatus = status === "comprobado" ? "confirmed" : status === "inferido" ? "inferred" : "pending";
      return `<span class="cap ${cls}" title="${name}: ${engStatus}">${label}</span>`;
    }).join(""); renderMessages();
    renderAttachments();
    updateContextBar();
    await loadSessions();
  } catch (e) {
    console.warn("createSession failed:", e);
  }
}

async function loadSession(id) {
  try {
    const response = await fetch(`/api/sessions/${encodeURIComponent(id)}`);
    if (!response.ok) return;
    const sess = await response.json();
    state.activeSessionId = sess.id;
    localStorage.setItem("ollamabot.activeSessionId", sess.id);
    state.messages = (sess.messages || []).map((m) => {
      // Normalize raw messages back to frontend shape
      const msg = typeof m === "string" ? JSON.parse(m) : m;
      return {
        role: msg.role || "user",
        content: msg.content || "",
        thinking: msg.thinking || "",
        images: msg.images || undefined,
        attachments: (msg.attachments || []).map((att) => {
          let url = att.url;
          if (!url || url === "undefined") {
            if (att.data) {
              const mime = att.mime || (att.kind === "audio" ? "audio/wav" : "image/png");
              url = `data:${mime};base64,${att.data}`;
            }
          }
          return {
            name: att.name || "",
            mime: att.mime || (att.kind === "audio" ? "audio/wav" : "image/png"),
            kind: att.kind || "",
            data: att.data || "",
            url: url || ""
          };
        }),
        toolCalls: msg.toolCalls || msg.tool_calls || [],
        toolResults: msg.toolResults || msg.tool_results || [],
        streaming: false,
        waiting: false,
      };
    });
    if (sess.model) state.activeModel = sess.model;
    renderMessages();
    renderAttachments();
    updateContextBar();
    renderSessions();
  } catch (e) {
    console.warn("loadSession failed:", e);
  }
}

async function saveSession() {
  if (!state.activeSessionId) return;
  try {
    const messages = state.messages.filter((msg) => msg.role !== "system").map((msg) => ({
      role: msg.role,
      content: msg.content || "",
      thinking: msg.thinking || "",
      images: msg.images || undefined,
      attachments: msg.attachments || undefined,
      image_kinds: msg.attachments?.map((a) => a.kind) || undefined,
      toolCalls: msg.toolCalls || [],
      toolResults: msg.toolResults || [],
    }));
    await fetch(`/api/sessions/${encodeURIComponent(state.activeSessionId)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ messages, model: state.activeModel }),
    });
    await loadSessions();
  } catch (e) {
    console.warn("saveSession failed:", e);
  }
}

function renderSessions() {
  if (!els.sessionList) return;
  els.sessionList.innerHTML = "";
  if (!state.sessions.length) {
    els.sessionList.innerHTML = `<div class="empty">No sessions yet</div>`;
    return;
  }
  for (const sess of state.sessions) {
    const btn = document.createElement("button");
    btn.className = `session-item ${sess.id === state.activeSessionId ? "active" : ""}`;
    btn.dataset.id = sess.id;
    const date = sess.updated_at ? new Date(sess.updated_at).toLocaleDateString() : "";
    btn.innerHTML = `<div class="session-info"><div class="session-title-row"><span class="session-title">${escapeHtml(sess.title || "Untitled")}</span><button class="session-rename-btn" type="button" title="Rename session">✏️</button></div><span class="session-meta">${escapeHtml(sess.model || "")} · ${escapeHtml(date)}</span></div><button class="session-delete" type="button" title="Delete session">×</button>`;
    els.sessionList.appendChild(btn);
  }
}

// ----- Context bar -----

function updateContextBar() {
  const model = activeModel();
  const ctxLen = model?.context_length || 0;
  if (!ctxLen || !els.contextFill || !els.contextLabel || !els.contextBar) return;
  let chars = 0;
  for (const msg of state.messages) {
    chars += (msg.content || "").length;
    chars += (msg.thinking || "").length;
  }
  const estimatedTokens = Math.ceil(chars / 4);
  const pct = Math.min(100, Math.round((estimatedTokens / ctxLen) * 100));
  els.contextFill.style.width = `${pct}%`;
  els.contextLabel.textContent = `${pct}%`;
  els.contextFill.classList.remove("medium", "high");
  if (pct >= 90) els.contextFill.classList.add("high");
  else if (pct >= 70) els.contextFill.classList.add("medium");
  els.contextBar.title = `${estimatedTokens.toLocaleString()} / ${ctxLen.toLocaleString()} tokens (${pct}%)`;
}

async function deleteSession(id) {
  try {
    const response = await fetch(`/api/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
    if (!response.ok) return;
    state.sessions = state.sessions.filter((s) => s.id !== id);
    if (state.activeSessionId === id) {
      state.activeSessionId = null;
      state.messages = [];
      state.attachments = [];
      renderMessages();
      renderAttachments();
    }
    renderSessions();
  } catch (e) {
    console.warn("deleteSession failed:", e);
  }
}

// Settings tab-switching wiring
document.querySelectorAll(".settings-tabs .tab-btn").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll(".settings-tabs .tab-btn").forEach((b) => b.classList.remove("active"));
    document.querySelectorAll(".settings-form-tabbed .tab-content").forEach((c) => c.classList.remove("active"));
    btn.classList.add("active");
    const target = btn.dataset.tab;
    document.getElementById(`tab-${target}`).classList.add("active");
  });
});

async function autoGenerateSessionTitle(assistantContent) {
  if (!state.activeSessionId) return;
  const id = state.activeSessionId;
  try {
    const response = await fetch("/api/chat/stream", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model: state.activeModel,
        messages: [
          {
            role: "system",
            content: "Summarize the main topic of the response in an extremely short title (2 to 4 words). Do not use quotation marks, punctuation, or explanations. Respond with only the title."
          },
          {
            role: "user",
            content: assistantContent
          }
        ],
        think: false,
      }),
    });
    if (!response.ok || !response.body) return;

    let generatedTitle = "";
    await readEventStream(response.body, {
      content: (value) => {
        generatedTitle += value;
      },
      done: async () => {
        generatedTitle = generatedTitle.trim().replace(/^["']|["']$/g, ""); // strip quotes
        if (generatedTitle) {
          try {
            await fetch(`/api/sessions/${encodeURIComponent(id)}`, {
              method: "PUT",
              headers: { "Content-Type": "application/json" },
              body: JSON.stringify({ title: generatedTitle }),
            });
            const session = state.sessions.find(s => s.id === id);
            if (session) session.title = generatedTitle;
            renderSessions();
          } catch (err) {
            console.warn("Auto-rename failed:", err);
          }
        }
      }
    });
  } catch (err) {
    console.warn("Auto-rename call failed:", err);
  }
}
