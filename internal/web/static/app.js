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
  projects: [],
  activeProjectId: null,
  isTicking: false,
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
  messageQueue: [],
  isProcessing: false,
  currentAbortController: null,
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
  skipBtn: document.querySelector("#skipBtn"),
  sendBtn: document.querySelector("#sendBtn"),
  
  // Projects DOM Elements
  openProjects: document.querySelector("#openProjects"),
  projectsDialog: document.querySelector("#projectsDialog"),
  addNewProjectBtn: document.querySelector("#addNewProjectBtn"),
  projectsList: document.querySelector("#projectsList"),
  projectsWelcomeState: document.querySelector("#projectsWelcomeState"),
  projectsCreateState: document.querySelector("#projectsCreateState"),
  projectsDetailState: document.querySelector("#projectsDetailState"),
  projectsLogReaderState: document.querySelector("#projectsLogReaderState"),
  welcomeNewProjBtn: document.querySelector("#welcomeNewProjBtn"),
  createProjectForm: document.querySelector("#createProjectForm"),
  projNameInput: document.querySelector("#projNameInput"),
  projGoalInput: document.querySelector("#projGoalInput"),
  cancelCreateProjBtn: document.querySelector("#cancelCreateProjBtn"),
  detailProjName: document.querySelector("#detailProjName"),
  detailProjStatus: document.querySelector("#detailProjStatus"),
  detailProjGoal: document.querySelector("#detailProjGoal"),
  detailTodosList: document.querySelector("#detailTodosList"),
  addTodoForm: document.querySelector("#addTodoForm"),
  newTodoInput: document.querySelector("#newTodoInput"),
  detailLogsList: document.querySelector("#detailLogsList"),
  triggerTickBtn: document.querySelector("#triggerTickBtn"),
  tickSpinner: document.querySelector("#tickSpinner"),
  tickBtnText: document.querySelector("#tickBtnText"),
  deleteProjectBtn: document.querySelector("#deleteProjectBtn"),
  backToDetailBtn: document.querySelector("#backToDetailBtn"),
  logReaderTitle: document.querySelector("#logReaderTitle"),
  logReaderContent: document.querySelector("#logReaderContent"),
};

// Bind Projects click handler
els.openProjects.addEventListener("click", () => {
  openProjectsDashboard();
});

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
  els.webExposeToggle.checked = !!state.settings.server_expose_network;
  els.webAutoNameToggle.checked = state.settings.session_auto_name !== false;
  els.webSearchSelect.value = state.settings.web_search_enabled ? "ddg" : "none";
  els.webPort.value = state.settings.server_port || "8080";
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
if (els.skipBtn) {
  els.skipBtn.addEventListener("click", () => {
    if (state.currentAbortController) {
      state.currentAbortController.abort();
    }
  });
}
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
  els.webExposeToggle.checked = !!state.settings.server_expose_network;
  els.webAutoNameToggle.checked = state.settings.session_auto_name !== false;
  els.webSearchSelect.value = state.settings.web_search_enabled ? "ddg" : "none";
  els.webPort.value = state.settings.server_port || "8080";
  if (state.settings.model_default) {
    state.activeModel = state.settings.model_default;
    localStorage.setItem("ollamabot.mainModel", state.activeModel);
  }
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
      model_default: state.activeModel,
      model_vision: state.visionModel,
      model_audio: state.audioModel,
      model_embeddings: state.embeddingsModel,
      web_search_enabled: els.webSearchSelect.value === "ddg",
      server_expose_network: els.webExposeToggle.checked,
      session_auto_name: els.webAutoNameToggle.checked,
      server_port: els.webPort.value.trim() || "8080",
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
      skills_path: state.settings.skills_path || "skills",
      model_default: state.activeModel,
      model_vision: state.visionModel,
      model_audio: state.audioModel,
      model_embeddings: state.embeddingsModel,
      web_search_enabled: state.settings.web_search_enabled || false,
      server_expose_network: state.settings.server_expose_network || false,
      session_auto_name: state.settings.session_auto_name !== false,
      server_port: state.settings.server_port || "8080",
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
    // If the user configured a dedicated role model, always trust it!
    return dedicated;
  }
  const main = activeModel();
  if (!main) return null;
  const status = main.capabilities?.[capKey];
  if (status === "comprobado" || status === "inferido") return main.name;
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
    const isMainCapable = canBeMain(model);
    const canVision = model.capabilities?.vision === "comprobado" || model.capabilities?.vision === "inferido";
    const canAudio = model.capabilities?.audio === "comprobado" || model.capabilities?.audio === "inferido";
    const canEmbed = model.capabilities?.embedding === "comprobado" || model.capabilities?.embedding === "inferido";

    const isMain = model.name === state.activeModel;
    const isVision = model.name === state.visionModel || (isMain && !state.visionModel && canVision);
    const isAudio = model.name === state.audioModel || (isMain && !state.audioModel && canAudio);
    const isEmbed = model.name === state.embeddingsModel;
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
        saveRoleModels();
        if (state.activeSessionId) {
          saveSession();
        }
        renderActive();
        renderModels();
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
    // Prevent keyboard events inside audio attachment cards from reaching the form
    card.addEventListener("keydown", (e) => e.stopPropagation());
    card.addEventListener("keypress", (e) => e.stopPropagation());
    card.addEventListener("keyup", (e) => e.stopPropagation());
    card.querySelector("button").addEventListener("click", () => {
      state.attachments.splice(index, 1);
      renderAttachments();
    });
    els.attachments.appendChild(card);
  }
}

async function sendMessage(event) {
  event.preventDefault();
  if (state.isRecording) {
    await stopRecording();
  }
  const content = els.prompt.value.trim();
  console.log("[sendMessage] Triggered. content_len:", content.length, "attachments:", state.attachments.length, "activeModel:", state.activeModel);
  if (state.attachments.length > 0) {
    console.log("[sendMessage] Attachment kinds:", state.attachments.map(a => a.kind), "data_lens:", state.attachments.map(a => a.data?.length || 0));
  }
  if ((!content && state.attachments.length === 0) || !state.activeModel) return;

  if (!state.activeSessionId) {
    const title = content ? content.slice(0, 40) : "New session";
    await createSession(title);
  }

  state.attachments = state.attachments.filter((attachment) => capabilityFor(attachment.kind));
  const images = state.attachments.map((attachment) => attachment.data);
  const visibleAttachments = [...state.attachments];
  
  // Push the message with processed = false to state
  const userMessage = { role: "user", content, images, attachments: visibleAttachments, processed: false };
  state.messages.push(userMessage);
  
  state.attachments = [];
  els.prompt.value = "";
  els.prompt.focus();
  renderAttachments();
  renderMessages();
  updateContextBar();
  if (visibleAttachments.length) {
    addSystemMessage(`Attached ${visibleAttachments.map((item) => item.kind).join(", ")} using Ollama multimodal payload.`);
  }

  // Push user query to client-side sequential queue
  state.messageQueue.push(userMessage);
  processNextQueueItem();
}

async function processNextQueueItem() {
  if (state.isProcessing || state.messageQueue.length === 0) {
    updateComposerUI();
    return;
  }

  state.isProcessing = true;
  updateComposerUI();

  const nextItem = state.messageQueue.shift();
  nextItem.processed = true;

  // Filter history up to and including the current user query!
  const outboundMessages = [];
  for (const msg of state.messages) {
    if (msg === nextItem) {
      outboundMessages.push({
        role: msg.role,
        content: msg.content || "",
        images: msg.images || undefined,
        image_kinds: msg.attachments?.map((a) => a.kind) || undefined,
      });
      break;
    }
    if (msg.role === "user" || msg.role === "assistant") {
      const hasAudio = msg.attachments?.some((a) => a.kind === "audio");
      const hasRoutedVision = msg.attachments?.some((a) => a.kind === "image") && state.settings?.model_vision;
      const shouldClearImages = hasAudio || hasRoutedVision;

      outboundMessages.push({
        role: msg.role,
        content: msg.content || "",
        images: shouldClearImages ? undefined : (msg.images || undefined),
        image_kinds: shouldClearImages ? undefined : (msg.attachments?.map((a) => a.kind) || undefined),
      });
    }
  }

  const assistant = { role: "assistant", content: "", steps: [], streaming: true, waiting: true, metrics: null };
  
  // Insert assistant response directly after the current user query in the messages list
  const idx = state.messages.indexOf(nextItem);
  if (idx !== -1) {
    state.messages.splice(idx + 1, 0, assistant);
  } else {
    state.messages.push(assistant);
  }
  renderMessages();

  state.currentAbortController = new AbortController();

  // Log what we're about to send
  const currentMsg = outboundMessages[outboundMessages.length - 1];
  console.log("[processQueue] Sending to /api/chat/stream:", {
    model: state.activeModel,
    totalMessages: outboundMessages.length,
    currentMsg: {
      role: currentMsg?.role,
      content_len: currentMsg?.content?.length || 0,
      images: currentMsg?.images?.length || 0,
      image_kinds: currentMsg?.image_kinds,
    }
  });

  try {
    const response = await fetch("/api/chat/stream", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        model: state.activeModel,
        messages: outboundMessages,
        think: els.think.checked,
      }),
      signal: state.currentAbortController.signal,
    });
    if (!response.ok || !response.body) {
      assistant.content = `Error: ${response.statusText}`;
      assistant.waiting = false;
      assistant.streaming = false;
      renderMessages();
      return;
    }
    await readEventStream(response.body, {
      media_pre_processing: (value) => {
        assistant.waiting = false;
        const mediaRouterMsg = {
          role: "assistant",
          content: value,
          streaming: false,
          waiting: false
        };
        const idx = state.messages.indexOf(assistant);
        if (idx !== -1) {
          state.messages.splice(idx, 0, mediaRouterMsg);
        } else {
          state.messages.unshift(mediaRouterMsg);
        }

        // Extract transcription/analysis and assign to nextItem.content to preserve context
        if (nextItem && !nextItem.content) {
          let textParts = [];
          const parts = value.split("\n\n");
          const transcriptions = [];
          const analyses = [];
          for (let i = 1; i < parts.length; i++) {
            const part = parts[i].trim();
            if (part.startsWith("[Audio Transcription & Analysis]:")) {
              transcriptions.push(part.slice("[Audio Transcription & Analysis]:".length).trim());
            } else if (part.startsWith("[Image Analysis")) {
              const closingIdx = part.indexOf(")]:");
              if (closingIdx !== -1) {
                analyses.push(part.slice(closingIdx + 3).trim());
              } else {
                analyses.push(part);
              }
            }
          }
          if (transcriptions.length > 0) {
            textParts.push(transcriptions.join("\n\n"));
          }
          if (analyses.length > 0) {
            textParts.push(analyses.join("\n\n"));
          }
          if (textParts.length > 0) {
            nextItem.content = textParts.join("\n\n");
          }
        }

        renderMessages();
      },
      thinking: (value) => {
        assistant.waiting = false;
        const lastStep = assistant.steps[assistant.steps.length - 1];
        if (lastStep && lastStep.type === "thinking") {
          lastStep.content += value;
        } else {
          assistant.steps.push({ type: "thinking", content: value });
        }
        renderMessages();
      },
      content: (value) => {
        assistant.waiting = false;
        assistant.content += value;
        renderMessages();
      },
      tool_call: (value) => {
        assistant.waiting = false;
        const fn = value?.function || {};
        const name = fn.name || "unknown";
        let step = assistant.steps.find(s => s.type === "tool_exec" && s.name === name && s.status === "running");
        if (!step) {
          step = { type: "tool_exec", name: name, arguments: fn.arguments, result: null, status: "running" };
          assistant.steps.push(step);
        } else {
          step.arguments = fn.arguments;
        }
        renderMessages();
      },
      tool_start: (value) => {
        assistant.waiting = true; // Show loading spinner while tool runs
        const name = value.name || "unknown";
        let step = assistant.steps.find(s => s.type === "tool_exec" && s.name === name && s.status === "running");
        if (!step) {
          step = { type: "tool_exec", name: name, arguments: value.arguments, result: null, status: "running" };
          assistant.steps.push(step);
        } else {
          step.arguments = value.arguments;
        }
        renderMessages();
      },
      tool_result: (value) => {
        assistant.waiting = true; // Keep loading spinner active until next round chunks arrive
        for (let i = assistant.steps.length - 1; i >= 0; i--) {
          const step = assistant.steps[i];
          if (step.type === "tool_exec" && step.name === value.name && step.status === "running") {
            step.result = value.result;
            step.status = "done";
            break;
          }
        }
        renderMessages();
      },
      error: (value) => {
        assistant.waiting = false;
        assistant.streaming = false;
        assistant.content += `\nError: ${value}`;
        renderMessages();
      },
      done: (value) => {
        // Accumulate performance metrics from intermediate Ollama done payloads
        if (value && value.total_duration) {
          if (!assistant.metrics) {
            assistant.metrics = {
              total_duration: 0,
              load_duration: 0,
              prompt_eval_count: 0,
              prompt_eval_duration: 0,
              eval_count: 0,
              eval_duration: 0,
            };
          }
          assistant.metrics.total_duration += (value.total_duration || 0);
          assistant.metrics.load_duration += (value.load_duration || 0);
          assistant.metrics.prompt_eval_count += (value.prompt_eval_count || 0);
          assistant.metrics.prompt_eval_duration += (value.prompt_eval_duration || 0);
          assistant.metrics.eval_count += (value.eval_count || 0);
          assistant.metrics.eval_duration += (value.eval_duration || 0);
        }
        const hasRunningTools = assistant.steps.some(s => s.type === "tool_exec" && s.status === "running");
        if (hasRunningTools) {
          assistant.waiting = true;
        }
        renderMessages();
      },
    });
  } catch (err) {
    if (err.name === "AbortError") {
      assistant.content += "\n\n*(Skipped/Paused by user)*";
    } else {
      assistant.content += `\nError: ${err.message}`;
    }
  } finally {
    // Stream is fully closed by the server. All rounds are complete!
    assistant.waiting = false;
    assistant.streaming = false;
    renderMessages();
    updateContextBar();
    // Clear binary base64 images from user messages that were pre-processed (transcribed/analyzed)
    // to prevent re-sending and reduce session size.
    if (nextItem && nextItem.role === "user") {
      const hasAudio = nextItem.attachments?.some((a) => a.kind === "audio");
      const hasRoutedVision = nextItem.attachments?.some((a) => a.kind === "image") && state.settings?.model_vision;
      if (hasAudio || hasRoutedVision) {
        nextItem.images = [];
      }
    }

    await saveSession();
    await loadModels();

    // Auto-generate session title if enabled and it's the first message exchange
    if (state.settings && state.settings.session_auto_name !== false) {
      const userMsgs = state.messages.filter((m) => m.role === "user");
      const assistantMsgs = state.messages.filter((m) => m.role === "assistant");
      console.log("[Auto-Name Check]", {
        session_auto_name: state.settings.session_auto_name,
        userMsgsCount: userMsgs.length,
        assistantMsgsCount: assistantMsgs.length,
        activeSessionId: state.activeSessionId
      });
      if (userMsgs.length === 1 && assistantMsgs.length === 1) {
        autoGenerateSessionTitle(assistant.content);
      }
    }

    state.isProcessing = false;
    state.currentAbortController = null;
    updateComposerUI();

    // Process next item in the queue!
    processNextQueueItem();
  }
}

function updateComposerUI() {
  if (state.isProcessing) {
    if (els.skipBtn) els.skipBtn.style.display = "inline-flex";
    if (els.sendBtn) els.sendBtn.textContent = "Queue";
  } else {
    if (els.skipBtn) els.skipBtn.style.display = "none";
    if (els.sendBtn) els.sendBtn.textContent = "Send";
  }
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

function renderPreProcessingContent(content) {
  const parts = content.split("\n\n");
  let html = `<div class="preprocessing-header"><span class="step-tool-icon">🧠</span> <strong>Media Pre-Processing Flow</strong></div>`;
  
  for (let i = 1; i < parts.length; i++) {
    const part = parts[i].trim();
    if (!part) continue;
    
    if (part.startsWith("[Audio Transcription & Analysis]:")) {
      const body = part.slice("[Audio Transcription & Analysis]:".length).trim();
      html += `
        <div class="analysis-box audio-analysis">
          <div class="analysis-box-head">
            <span class="analysis-icon">🎙️</span>
            <strong>Audio Transcription & Analysis</strong>
            <span class="analysis-tag">role model: audio</span>
          </div>
          <div class="analysis-box-body">${renderMarkdown(body)}</div>
        </div>
      `;
    } else if (part.startsWith("[Image Analysis (Prompt:")) {
      const closingBracketIndex = part.indexOf(")]:");
      let promptText = "";
      let body = part;
      if (closingBracketIndex !== -1) {
        promptText = part.slice("[Image Analysis (Prompt: ".length, closingBracketIndex).trim();
        body = part.slice(closingBracketIndex + 3).trim();
      }
      
      html += `
        <div class="analysis-box image-analysis">
          <div class="analysis-box-head">
            <span class="analysis-icon">🖼️</span>
            <strong>Image Context Analysis</strong>
            <span class="analysis-tag">role model: vision</span>
          </div>
          ${promptText ? `<div class="analysis-box-prompt"><strong>Instruction:</strong> <em>${escapeHtml(promptText)}</em></div>` : ""}
          <div class="analysis-box-body">${renderMarkdown(body)}</div>
        </div>
      `;
    } else {
      html += `
        <div class="analysis-box general-analysis">
          <div class="analysis-box-body">${renderMarkdown(part)}</div>
        </div>
      `;
    }
  }
  return `<div class="preprocessing-wrapper">${html}</div>`;
}

function renderMessages() {
  els.messages.innerHTML = "";
  for (const message of state.messages) {
    if (message.role === "system") continue;
    const div = document.createElement("article");
    const isQueued = message.role === "user" && message.processed === false;
    const isPreProcessing = message.role === "assistant" && message.content && message.content.startsWith("The user has attached media. The pre-processing analysis is as follows:");
    
    div.className = `message ${message.role} ${message.streaming ? "streaming" : ""} ${isQueued ? "queued" : ""} ${isPreProcessing ? "preprocessing" : ""}`;
    const pending = message.waiting ? `<div class="waiting"><span></span><span></span><span></span><em>processing</em></div>` : "";
    const media = message.attachments?.length ? `<div class="message-media">${message.attachments.map(attachmentPreview).join("")}</div>` : "";
    const cursor = message.streaming ? `<span class="stream-cursor"></span>` : "";
    
    // Build steps HTML (interleaved thinking / tool blocks).
    const stepsHtml = (message.steps || []).map(renderStep).join("");
    // Legacy fallback: if no steps but has old-style thinking/toolCalls/toolResults, render them.
    let legacyHtml = "";
    if (!message.steps?.length) {
      if (message.thinking) {
        legacyHtml += `<details class="step step-thinking" open><summary>💭 thinking</summary><pre>${escapeHtml(message.thinking)}</pre></details>`;
      }
      if (message.toolCalls?.length) {
        legacyHtml += message.toolCalls.map(renderLegacyToolCall).join("");
      }
      if (message.toolResults?.length) {
        legacyHtml += message.toolResults.map(renderLegacyToolResult).join("");
      }
    }
    
    let metricsHtml = "";
    if (message.metrics && message.metrics.total_duration) {
      const m = message.metrics;
      const totalSec = (m.total_duration / 1e9).toFixed(2);
      const evalSec = (m.eval_duration / 1e9).toFixed(2);
      const promptSec = (m.prompt_eval_duration / 1e9).toFixed(2);
      const loadSec = (m.load_duration / 1e9).toFixed(2);
      const tokensPerSec = m.eval_duration > 0 ? (m.eval_count / (m.eval_duration / 1e9)).toFixed(1) : "0.0";
      metricsHtml = `
        <div class="message-metrics">
          <span title="Total time taken">🕒 ${totalSec}s</span>
          <span title="Generation speed">⚡ ${tokensPerSec} t/s</span>
          <span title="Generated tokens / Eval duration">📤 ${m.eval_count} tokens (${evalSec}s)</span>
          <span title="Prompt tokens / Eval duration">📥 ${m.prompt_eval_count} prompt (${promptSec}s)</span>
          ${m.load_duration > 0 ? `<span title="Model load time">💾 load ${loadSec}s</span>` : ""}
        </div>
      `;
    }
    
    const queuedBadge = isQueued ? ` <span class="queued-badge">⏳ In Queue</span>` : "";
    let contentHtml = "";
    let roleName = message.role;
    if (isPreProcessing) {
      roleName = "media router";
      contentHtml = renderPreProcessingContent(message.content);
    } else {
      contentHtml = `<div class="markdown">${renderMarkdown(message.content || "")}${cursor}</div>`;
    }
    
    div.innerHTML = `<span class="role">${escapeHtml(roleName)}${queuedBadge}</span>${media}${pending}${stepsHtml || legacyHtml}${contentHtml}${metricsHtml}`;
    els.messages.appendChild(div);
  }
  els.messages.scrollTop = els.messages.scrollHeight;
}

function renderStep(step) {
  switch (step.type) {
    case "thinking":
      return `<details class="step step-thinking" open><summary>💭 thinking</summary><pre>${escapeHtml(step.content || "")}</pre></details>`;
    case "tool_call": {
      const fn = step.call?.function || {};
      const name = fn.name || "unknown";
      let argsText = "";
      try {
        const parsed = typeof fn.arguments === "string" ? JSON.parse(fn.arguments) : (fn.arguments || {});
        argsText = JSON.stringify(parsed, null, 2);
      } catch {
        argsText = String(fn.arguments || "{}");
      }
      return `<div class="step step-tool-call"><span class="step-tool-icon">🔧</span> <strong>${escapeHtml(name)}</strong><pre>${escapeHtml(argsText)}</pre></div>`;
    }
    case "tool_exec": {
      const statusLabel = step.status === "running" ? "running..." : "done";
      const statusClass = step.status === "running" ? "running" : "done";
      let argsText = "";
      if (step.arguments) {
        try {
          const parsed = typeof step.arguments === "string" ? JSON.parse(step.arguments) : step.arguments;
          argsText = JSON.stringify(parsed, null, 2);
        } catch {
          argsText = String(step.arguments || "");
        }
      }
      const resultText = step.result !== null && step.result !== undefined ? escapeHtml(String(step.result)) : "";
      const argsHtml = argsText ? `<pre class="step-tool-args">${escapeHtml(argsText)}</pre>` : "";
      const resultHtml = resultText ? `
        <details class="step-tool-result-details">
          <summary>📄 Show tool response (${formatBytes(resultText.length)})</summary>
          <pre class="step-tool-result-text">${resultText}</pre>
        </details>
      ` : (step.status === "running" ? `<div class="step-tool-running"><span></span><span></span><span></span></div>` : "");
      return `<details class="step step-tool-exec ${statusClass}" open><summary><span class="step-tool-icon">⚙️</span> ${escapeHtml(step.name || "unknown")} <span class="step-tool-status ${statusClass}">${statusLabel}</span></summary>${argsHtml}${resultHtml}</details>`;
    }
    default:
      return "";
  }
}

function renderLegacyToolCall(call) {
  const fn = call.function || {};
  return `<div class="step step-tool-call"><span class="step-tool-icon">🔧</span> <strong>${escapeHtml(fn.name || "unknown")}</strong><pre>${escapeHtml(JSON.stringify(fn.arguments || {}, null, 2))}</pre></div>`;
}

function renderLegacyToolResult(tr) {
  const status = tr.status === "running" ? "running..." : "done";
  const statusClass = tr.status === "running" ? "running" : "done";
  const result = tr.result !== null ? escapeHtml(String(tr.result)) : "";
  return `<details class="step step-tool-exec ${statusClass}" open><summary><span class="step-tool-icon">⚙️</span> ${escapeHtml(tr.name || "unknown")} <span class="step-tool-status ${statusClass}">${status}</span></summary><pre class="step-tool-result-text">${result}</pre></details>`;
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
    // Prevent ALL events from audio controls from propagating to parent elements.
    // This avoids issues where interacting with native audio controls (play, pause,
    // seek, volume) could interfere with form submission or steal focus from prompt.
    const stopAll = `onclick="event.stopPropagation(); event.stopImmediatePropagation()" onkeydown="event.stopPropagation(); event.stopImmediatePropagation()" onkeypress="event.stopPropagation(); event.stopImmediatePropagation()" onkeyup="event.stopPropagation(); event.stopImmediatePropagation()" onmousedown="event.stopPropagation()" onmouseup="event.stopPropagation()" onpointerdown="event.stopPropagation()" onpointerup="event.stopPropagation()" onfocus="event.stopPropagation()"`;
    return `<div class="media-preview audio" ${stopAll}><span>${label}</span><audio controls preload="metadata" src="${escapeAttr(attachment.url)}" ${stopAll}></audio></div>`;
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
    if (state.audioContext.state === "suspended") {
      await state.audioContext.resume();
    }
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
    renderMessages();
    renderAttachments();
    updateContextBar();
    await loadSessions();
    els.prompt.focus();
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
      // Migrate legacy thinking/toolCalls/toolResults to steps format.
      let steps = msg.steps || [];
      if (!steps.length) {
        if (msg.thinking) {
          steps.push({ type: "thinking", content: msg.thinking });
        }
        const tc = msg.toolCalls || msg.tool_calls || [];
        const tr = msg.toolResults || msg.tool_results || [];
        for (const call of tc) {
          steps.push({ type: "tool_call", call });
        }
        for (const res of tr) {
          steps.push({ type: "tool_exec", name: res.name, arguments: res.arguments, result: res.result, status: res.status || "done" });
        }
      }
      return {
        role: msg.role || "user",
        content: msg.content || "",
        steps,
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
        streaming: false,
        waiting: false,
      };
    });
    if (sess.model) state.activeModel = sess.model;
    renderMessages();
    renderAttachments();
    updateContextBar();
    renderSessions();
    els.prompt.focus();
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
      steps: msg.steps || [],
      images: msg.images || undefined,
      attachments: msg.attachments || undefined,
      image_kinds: msg.attachments?.map((a) => a.kind) || undefined,
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
    // Count thinking text from steps.
    for (const step of (msg.steps || [])) {
      if (step.type === "thinking") chars += (step.content || "").length;
    }
    // Legacy fallback.
    if (!msg.steps?.length) chars += (msg.thinking || "").length;
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
  console.log("[Auto-Name] Triggered for session ID:", id, "Content length:", assistantContent?.length);
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
    
    console.log("[Auto-Name] Response status:", response.status);
    if (!response.ok || !response.body) {
      console.warn("[Auto-Name] Failed response from /api/chat/stream:", response.statusText);
      return;
    }

    let generatedTitle = "";
    await readEventStream(response.body, {
      content: (value) => {
        generatedTitle += value;
        console.log("[Auto-Name] Stream chunk content:", value);
      },
      done: () => {
        console.log("[Auto-Name] Server sent 'done' event chunk.");
      }
    });

    console.log("[Auto-Name] Stream completely read. Raw title:", generatedTitle);
    generatedTitle = generatedTitle.trim().replace(/^["']|["']$/g, "").replace(/[.!?]+$/, ""); // strip quotes and trailing punctuation
    if (generatedTitle) {
      console.log("[Auto-Name] Saving generated title:", generatedTitle);
      try {
        const putResp = await fetch(`/api/sessions/${encodeURIComponent(id)}`, {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ title: generatedTitle }),
        });
        console.log("[Auto-Name] Save title response status:", putResp.status);
        if (putResp.ok) {
          const session = state.sessions.find(s => s.id === id);
          if (session) {
            session.title = generatedTitle;
            console.log("[Auto-Name] Updated active session title in state.sessions:", generatedTitle);
          }
          renderSessions();
        } else {
          console.warn("[Auto-Name] Failed to save title via PUT:", putResp.statusText);
        }
      } catch (err) {
        console.warn("[Auto-Name] Auto-rename PUT failed:", err);
      }
    } else {
      console.log("[Auto-Name] Generated title was empty, skipping rename.");
    }
  } catch (err) {
    console.warn("[Auto-Name] Auto-rename call overall failed:", err);
  }
}

/* ==========================================================================
   Autonomous Projects Agent (APA) Frontend Integration
   ========================================================================== */

function openProjectsDashboard() {
  els.projectsDialog.showModal();
  switchProjectsState("welcome");
  loadProjects();
}

function switchProjectsState(stateName) {
  const states = {
    welcome: els.projectsWelcomeState,
    create: els.projectsCreateState,
    detail: els.projectsDetailState,
    logReader: els.projectsLogReaderState,
  };
  
  for (const name in states) {
    if (states[name]) {
      if (name === stateName) {
        states[name].classList.add("active");
      } else {
        states[name].classList.remove("active");
      }
    }
  }
}

async function loadProjects() {
  els.projectsList.innerHTML = `<div class="empty">Loading projects...</div>`;
  try {
    const res = await fetch("/api/autonomous/projects");
    if (!res.ok) throw new Error("Failed to load projects");
    state.projects = await res.json();
    renderProjectsList();
  } catch (err) {
    els.projectsList.innerHTML = `<div class="empty error">Error: ${err.message}</div>`;
  }
}

function renderProjectsList() {
  els.projectsList.innerHTML = "";
  if (state.projects.length === 0) {
    els.projectsList.innerHTML = `<div class="empty">No projects active</div>`;
    return;
  }
  
  state.projects.forEach((proj) => {
    const item = document.createElement("div");
    const isSelected = state.activeProjectId === proj.id;
    item.className = `project-nav-item ${isSelected ? "selected" : ""} ${proj.status}`;
    item.innerHTML = `
      <div class="project-nav-main">
        <span class="project-nav-title">${escapeHtml(proj.name)}</span>
        <span class="project-nav-status">${proj.status}</span>
      </div>
      <div class="project-nav-goal">${escapeHtml(proj.goal)}</div>
    `;
    item.addEventListener("click", () => selectProject(proj.id));
    els.projectsList.appendChild(item);
  });
}

async function selectProject(id) {
  state.activeProjectId = id;
  renderProjectsList();
  switchProjectsState("detail");
  
  els.detailProjName.textContent = "Loading...";
  els.detailProjStatus.textContent = "";
  els.detailProjGoal.textContent = "";
  els.detailTodosList.innerHTML = `<div class="empty">Loading tasks...</div>`;
  els.detailLogsList.innerHTML = `<div class="empty">Loading logs...</div>`;
  
  try {
    const res = await fetch(`/api/autonomous/projects/${id}`);
    if (!res.ok) throw new Error("Failed to load project details");
    const data = await res.json();
    const proj = data.project;
    const logs = data.logs || [];
    
    // Update header
    els.detailProjName.textContent = proj.name;
    els.detailProjStatus.textContent = proj.status;
    els.detailProjStatus.className = `project-badge ${proj.status}`;
    els.detailProjGoal.textContent = proj.goal;
    
    // Render todos checklist
    renderProjectTodos(proj);
    
    // Render tick logs list
    renderProjectLogs(id, logs);
    
    // Enable/disable tick button based on status
    updateTickButtonUI(proj);
    
  } catch (err) {
    els.detailProjName.textContent = "Error";
    els.detailProjGoal.textContent = err.message;
  }
}

function renderProjectTodos(proj) {
  els.detailTodosList.innerHTML = "";
  if (!proj.todos || proj.todos.length === 0) {
    els.detailTodosList.innerHTML = `<div class="empty">No tasks defined</div>`;
    return;
  }
  
  proj.todos.forEach((todo) => {
    const item = document.createElement("div");
    item.className = `todo-item-row ${todo.status}`;
    
    let icon = "⏳";
    if (todo.status === "completed") icon = "✅";
    if (todo.status === "in_progress") icon = "🌀";
    if (todo.status === "failed") icon = "❌";
    
    item.innerHTML = `
      <span class="todo-icon">${icon}</span>
      <div class="todo-content-block">
        <span class="todo-task-text">${escapeHtml(todo.content)}</span>
        ${todo.result ? `<div class="todo-result-preview">${escapeHtml(todo.result.slice(0, 100))}...</div>` : ""}
      </div>
    `;
    els.detailTodosList.appendChild(item);
  });
}

function renderProjectLogs(projectId, logs) {
  els.detailLogsList.innerHTML = "";
  if (!logs || logs.length === 0) {
    els.detailLogsList.innerHTML = `<div class="empty">No execution logs yet</div>`;
    return;
  }
  
  // Sort logs descending (newest first)
  logs.sort((a, b) => b.localeCompare(a));
  
  logs.forEach((logName) => {
    const item = document.createElement("div");
    item.className = "log-item-row";
    
    // Parse friendly date/time from filename e.g. heartbeat_task-1_20260527_003000.md
    let friendlyName = logName;
    const parts = logName.split("_");
    if (parts.length >= 4) {
      const taskId = parts[1];
      const datePart = parts[2];
      const timePart = parts[3].replace(".md", "");
      if (datePart.length === 8 && timePart.length === 6) {
        const year = datePart.slice(0,4);
        const month = datePart.slice(4,6);
        const day = datePart.slice(6,8);
        const hour = timePart.slice(0,2);
        const min = timePart.slice(2,4);
        friendlyName = `${taskId} • ${day}/${month}/${year} ${hour}:${min}`;
      }
    }
    
    item.innerHTML = `
      <span class="log-file-icon">📄</span>
      <span class="log-file-name">${escapeHtml(friendlyName)}</span>
    `;
    item.addEventListener("click", () => readProjectLog(projectId, logName));
    els.detailLogsList.appendChild(item);
  });
}

async function readProjectLog(projectId, logName) {
  switchProjectsState("logReader");
  els.logReaderTitle.textContent = "Loading execution log...";
  els.logReaderContent.innerHTML = `<div class="empty">Fetching log file...</div>`;
  
  try {
    const res = await fetch(`/api/autonomous/projects/${projectId}/logs/${logName}`);
    if (!res.ok) throw new Error("Could not read log file");
    const markdown = await res.text();
    els.logReaderTitle.textContent = logName.replace(".md", "");
    els.logReaderContent.innerHTML = renderSimpleMarkdown(markdown);
  } catch (err) {
    els.logReaderTitle.textContent = "Error";
    els.logReaderContent.innerHTML = `<div class="empty error">${err.message}</div>`;
  }
}

function renderSimpleMarkdown(md) {
  if (!md) return "";
  let html = md;
  // Escape HTML tags to prevent injections
  html = html
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;");
  
  // Titles
  html = html.replace(/^# (.*?)$/gm, "<h1>$1</h1>");
  html = html.replace(/^## (.*?)$/gm, "<h2>$1</h2>");
  html = html.replace(/^### (.*?)$/gm, "<h3>$1</h3>");
  html = html.replace(/^#### (.*?)$/gm, "<h4>$1</h4>");
  
  // Lists
  html = html.replace(/^\- (.*?)$/gm, "<li>$1</li>");
  html = html.replace(/(<li>.*?<\/li>)/gs, "<ul>$1</ul>");
  html = html.replace(/<\/ul>\s*<ul>/g, ""); // deduplicate nested list wraps

  // Formatting (Bold and Inline Code)
  html = html.replace(/\*\*(.*?)\*\*/g, "<strong>$1</strong>");
  html = html.replace(/`(.*?)`/g, "<code>$1</code>");
  
  // Interactive Details panel
  html = html.replace(/&lt;details&gt;\s*&lt;summary&gt;(.*?)&lt;\/summary&gt;/g, "<details><summary>$1</summary><div class='details-inner'>");
  html = html.replace(/&lt;\/details&gt;/g, "</div></details>");
  
  // Paragraph gaps
  html = html.replace(/\n/g, "<br>");
  return html;
}

function updateTickButtonUI(proj) {
  if (state.isTicking) {
    els.triggerTickBtn.disabled = true;
    els.tickSpinner.style.display = "inline-block";
    els.tickBtnText.textContent = "Autonomous Heartbeat Running...";
    els.deleteProjectBtn.disabled = true;
    els.addTodoForm.querySelector("button").disabled = true;
  } else {
    els.deleteProjectBtn.disabled = false;
    els.addTodoForm.querySelector("button").disabled = false;
    els.tickSpinner.style.display = "none";
    
    const hasPending = proj.todos && proj.todos.some(t => t.status === "pending" || t.status === "in_progress");
    if (!hasPending) {
      els.triggerTickBtn.disabled = true;
      els.tickBtnText.textContent = "Project Completed!";
    } else {
      els.triggerTickBtn.disabled = false;
      els.tickBtnText.textContent = "Iterate Heartbeat Now";
    }
  }
}

// Event bindings for projects modal actions
els.addNewProjectBtn.addEventListener("click", () => {
  els.projNameInput.value = "";
  els.projGoalInput.value = "";
  switchProjectsState("create");
});

els.welcomeNewProjBtn.addEventListener("click", () => {
  els.projNameInput.value = "";
  els.projGoalInput.value = "";
  switchProjectsState("create");
});

els.cancelCreateProjBtn.addEventListener("click", () => {
  if (state.activeProjectId) {
    switchProjectsState("detail");
  } else {
    switchProjectsState("welcome");
  }
});

els.createProjectForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  const name = els.projNameInput.value.trim();
  const goal = els.projGoalInput.value.trim();
  if (!name || !goal) return;
  
  const submitBtn = els.createProjectForm.querySelector("button[type='submit']");
  submitBtn.disabled = true;
  const originalText = submitBtn.textContent;
  submitBtn.textContent = "Creating and planning sequential tasks...";
  
  try {
    const res = await fetch("/api/autonomous/projects", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name, goal }),
    });
    if (!res.ok) {
      const errData = await res.json();
      throw new Error(errData.error || "Failed to initialize project");
    }
    const newProj = await res.json();
    await loadProjects();
    selectProject(newProj.id);
  } catch (err) {
    alert(`Could not create project: ${err.message}`);
  } finally {
    submitBtn.disabled = false;
    submitBtn.textContent = originalText;
  }
});

els.addTodoForm.addEventListener("submit", async (e) => {
  e.preventDefault();
  if (!state.activeProjectId) return;
  
  const input = els.newTodoInput;
  const content = input.value.trim();
  if (!content) return;
  
  input.disabled = true;
  try {
    const res = await fetch(`/api/autonomous/projects/${state.activeProjectId}/todos`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ content }),
    });
    if (!res.ok) throw new Error("Could not add task");
    input.value = "";
    // Reload details
    selectProject(state.activeProjectId);
  } catch (err) {
    alert(err.message);
  } finally {
    input.disabled = false;
    input.focus();
  }
});

els.triggerTickBtn.addEventListener("click", async () => {
  if (!state.activeProjectId || state.isTicking) return;
  
  state.isTicking = true;
  const proj = state.projects.find(p => p.id === state.activeProjectId);
  if (proj) updateTickButtonUI(proj);
  
  try {
    const res = await fetch(`/api/autonomous/projects/${state.activeProjectId}/tick`, {
      method: "POST"
    });
    if (!res.ok) {
      const errData = await res.json();
      throw new Error(errData.error || "Heartbeat execution cycle failed");
    }
    const data = await res.json();
    
    // Reload projects state and select project to display the completed task status
    await loadProjects();
    await selectProject(state.activeProjectId);
    
    // Automatically select and display the newly created tick log for the user to read
    const logsRes = await fetch(`/api/autonomous/projects/${state.activeProjectId}`);
    if (logsRes.ok) {
      const details = await logsRes.json();
      if (details.logs && details.logs.length > 0) {
        details.logs.sort((a, b) => b.localeCompare(a));
        readProjectLog(state.activeProjectId, details.logs[0]);
      }
    }
  } catch (err) {
    alert(`Heartbeat tick failed: ${err.message}`);
    // Reload project status in case it changed
    await selectProject(state.activeProjectId);
  } finally {
    state.isTicking = false;
    const reloadedProj = state.projects.find(p => p.id === state.activeProjectId);
    if (reloadedProj) updateTickButtonUI(reloadedProj);
  }
});

els.deleteProjectBtn.addEventListener("click", async () => {
  if (!state.activeProjectId) return;
  if (!confirm("Are you sure you want to delete this autonomous project permanently? All code files in its workspace directory and all execution logs will be erased.")) return;
  
  try {
    const res = await fetch(`/api/autonomous/projects/${state.activeProjectId}`, {
      method: "DELETE"
    });
    if (!res.ok) throw new Error("Could not delete project");
    
    state.activeProjectId = null;
    await loadProjects();
    switchProjectsState("welcome");
  } catch (err) {
    alert(err.message);
  }
});

els.backToDetailBtn.addEventListener("click", () => {
  if (state.activeProjectId) {
    selectProject(state.activeProjectId);
  } else {
    switchProjectsState("welcome");
  }
});

