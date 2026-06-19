// Global fetch wrapper for Web Password Authentication
const originalFetch = window.fetch;
window.fetch = async function(resource, options) {
  options = options || {};
  options.headers = options.headers || {};
  
  const url = typeof resource === 'string' ? resource : resource.url;
  if (url && (url.startsWith('/api/') || url.includes('/api/'))) {
    const serverPassword = localStorage.getItem("ollamabot.serverPassword") || "";
    if (serverPassword) {
      if (options.headers instanceof Headers) {
        options.headers.set("X-Server-Password", serverPassword);
      } else {
        options.headers["X-Server-Password"] = serverPassword;
      }
    }
  }
  
  const response = await originalFetch(resource, options);
  
  if (response.status === 401 && url && !url.includes('/api/health')) {
    localStorage.removeItem("ollamabot.serverPassword");
    showLoginOverlay();
  }
  
  return response;
};

function showLoginOverlay() {
  const overlay = document.getElementById("loginOverlay");
  if (overlay) {
    overlay.style.display = "flex";
    const passwordInput = document.getElementById("loginPassword");
    if (passwordInput) {
      passwordInput.focus();
    }
  }
}

function hideLoginOverlay() {
  const overlay = document.getElementById("loginOverlay");
  if (overlay) {
    overlay.style.display = "none";
  }
}

document.addEventListener("DOMContentLoaded", () => {
  const loginForm = document.getElementById("loginForm");
  if (loginForm) {
    loginForm.addEventListener("submit", async (e) => {
      e.preventDefault();
      const passwordInput = document.getElementById("loginPassword");
      const errorDiv = document.getElementById("loginError");
      const pass = passwordInput ? passwordInput.value.trim() : "";
      
      if (!pass) return;
      
      try {
        const res = await originalFetch("/api/settings", {
          headers: { "X-Server-Password": pass }
        });
        if (res.ok) {
          localStorage.setItem("ollamabot.serverPassword", pass);
          hideLoginOverlay();
          window.location.reload();
        } else {
          if (errorDiv) {
            errorDiv.textContent = "Incorrect password. Please try again.";
            errorDiv.style.display = "block";
          }
          if (passwordInput) {
            passwordInput.value = "";
            passwordInput.focus();
          }
        }
      } catch (err) {
        if (errorDiv) {
          errorDiv.textContent = `Error: ${err.message || "failed to connect"}`;
          errorDiv.style.display = "block";
        }
      }
    });
  }
});

const state = {
  models: [],
  activeModel: "",
  visionModel: localStorage.getItem("ollamabot.visionModel") || "",
  audioModel: localStorage.getItem("ollamabot.audioModel") || "",
  embeddingsModel: localStorage.getItem("ollamabot.embeddingsModel") || "",
  imageModel: localStorage.getItem("ollamabot.imageModel") || "",
  imageSteps: parseInt(localStorage.getItem("ollamabot.imageSteps"), 10) || 4,
  learningModel: localStorage.getItem("ollamabot.learningModel") || "",
  subagentModel: localStorage.getItem("ollamabot.subagentModel") || "",
  messages: [],
  attachments: [],
  activePlan: null,
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
  sessionSearchQuery: "",
  modelActiveFilter: "all",
  sessionIdToDelete: null,
  messageQueue: [],
  isProcessing: false,
  currentAbortController: null,
  currentApprovalId: null,
  bootstrapReady: false,
};

let bootstrapPromise = null;

function waitForBootstrap() {
  return bootstrapPromise || Promise.resolve();
}

const els = {
  messages: document.querySelector("#messages"),
  form: document.querySelector("#chatForm"),
  prompt: document.querySelector("#prompt"),
  baseUrl: document.querySelector("#baseUrl"),
  version: document.querySelector("#version"),
  cacheState: document.querySelector("#cacheState"),
  memoryState: document.querySelector("#memoryState"),
  imageControl: document.querySelector("#imageControl"),
  audioControl: document.querySelector("#audioControl"),
  fileControl: document.querySelector("#fileControl"),
  imageInput: document.querySelector("#imageInput"),
  audioInput: document.querySelector("#audioInput"),
  fileInput: document.querySelector("#fileInput"),
  capabilityBar: document.querySelector("#capabilityBar"),
  capabilityBadges: document.querySelector("#capabilityBadges"),
  attachments: document.querySelector("#attachments"),
  modelsDialog: document.querySelector("#modelsDialog"),
  settingsDialog: document.querySelector("#settingsDialog"),
  imageDialog: document.querySelector("#imageDialog"),
  imageDialogImg: document.querySelector("#imageDialogImg"),
  modelsBody: document.querySelector("#modelsBody"),
  reloadModelsBtn: document.querySelector("#reloadModelsBtn"),
  reloadSpinner: document.querySelector("#reloadSpinner"),
  openModels: document.querySelector("#openModels"),
  openSettings: document.querySelector("#openSettings"),
  settingsForm: document.querySelector("#settingsForm"),
  ollamaUrl: document.querySelector("#ollamaUrl"),
  ollamaProbeModels: document.querySelector("#ollamaProbeModels"),
  ollamaImageSteps: document.querySelector("#ollamaImageSteps"),
  ollamaThinkToggle: document.querySelector("#ollamaThinkToggle"),
  workspacePath: document.querySelector("#workspacePath"),
  sessionsPath: document.querySelector("#sessionsPath"),
  memoryPath: document.querySelector("#memoryPath"),
  skillsPath: document.querySelector("#skillsPath"),
  webPort: document.querySelector("#webPort"),
  serverEnabled: document.querySelector("#serverEnabled"),
  serverPassword: document.querySelector("#serverPassword"),
  logoutBtn: document.querySelector("#logoutBtn"),
  webSearchToggle: document.querySelector("#webSearchToggle"),
  searchProvidersContainer: document.querySelector("#searchProvidersContainer"),
  searchProvidersList: document.querySelector("#searchProvidersList"),
  webExposeToggle: document.querySelector("#webExposeToggle"),
  webAutoNameToggle: document.querySelector("#webAutoNameToggle"),
  sessionExpiry: document.querySelector("#sessionExpiry"),
  telegramBotToken: document.querySelector("#telegramBotToken"),
  telegramAuthorizedIds: document.querySelector("#telegramAuthorizedIds"),
  telegramStartupNotification: document.querySelector("#telegramStartupNotification"),
  sleepModeToggle: document.querySelector("#sleepModeToggle"),
  sleepModeInactivity: document.querySelector("#sleepModeInactivity"),
  sleepModeResumeDelay: document.querySelector("#sleepModeResumeDelay"),
  sleepModeContainer: document.querySelector("#sleepModeContainer"),
  sleepModeSubagentsToggle: document.querySelector("#sleepModeSubagentsToggle"),
  recordControl: document.querySelector("#recordControl"),
  micSelect: document.querySelector("#micSelect"),
  sidebar: document.querySelector("#sidebar"),
  sessionList: document.querySelector("#sessionList"),
  sessionSearch: document.querySelector("#sessionSearch"),
  newSessionBtn: document.querySelector("#newSessionBtn"),
  toggleSidebar: document.querySelector("#toggleSidebar"),
  contextFill: document.querySelector("#contextFill"),
  contextLabel: document.querySelector("#contextLabel"),
  contextBar: document.querySelector("#contextBar"),
  appLayout: document.querySelector(".app-layout"),
  confirmDialog: document.querySelector("#confirmDialog"),
  confirmEyebrow: document.querySelector("#confirmEyebrow"),
  confirmTitle: document.querySelector("#confirmTitle"),
  confirmMessage: document.querySelector("#confirmMessage"),
  cancelConfirmBtn: document.querySelector("#cancelConfirmBtn"),
  okConfirmBtn: document.querySelector("#okConfirmBtn"),
  toast: document.querySelector("#toast"),
  skipBtn: document.querySelector("#skipBtn"),
  sendBtn: document.querySelector("#sendBtn"),
  approvalDialog: document.querySelector("#approvalDialog"),
  approvalToolName: document.querySelector("#approvalToolName"),
  approvalToolArgs: document.querySelector("#approvalToolArgs"),
  approveToolBtn: document.querySelector("#approveToolBtn"),
  denyToolBtn: document.querySelector("#denyToolBtn"),
  clarificationCard: document.querySelector("#clarificationCard"),
  clarificationQuestion: document.querySelector("#clarificationQuestion"),
  clarificationOptionsContainer: document.querySelector("#clarificationOptionsContainer"),
  clarificationCancel: document.querySelector("#clarificationCancel"),
  clarificationCustomInput: document.querySelector("#clarificationCustomInput"),
  clarificationSendCustom: document.querySelector("#clarificationSendCustom"),
  planConfirmationSelect: document.querySelector("#planConfirmationSelect"),
  planConfirmationCard: document.querySelector("#planConfirmationCard"),
  planSummaryText: document.querySelector("#planSummaryText"),
  planStepsContainer: document.querySelector("#planStepsContainer"),
  planRejectBtn: document.querySelector("#planRejectBtn"),
  planApproveBtn: document.querySelector("#planApproveBtn"),
  planCountdownContainer: document.querySelector("#planCountdownContainer"),
  planCountdown: document.querySelector("#planCountdown"),
  
  // Memory DOM Elements
  openMemory: document.querySelector("#openMemory"),
  memoryDialog: document.querySelector("#memoryDialog"),
  memoryTextDialog: document.querySelector("#memoryTextDialog"),
  memoryTextDialogContent: document.querySelector("#memoryTextDialogContent"),
  memorySearch: document.querySelector("#memorySearch"),
  testSearchBtn: document.querySelector("#testSearchBtn"),
  reindexMemoryBtn: document.querySelector("#reindexMemoryBtn"),
  reindexStatusArea: document.querySelector("#reindexStatusArea"),
  reindexSpinner: document.querySelector("#reindexSpinner"),
  memoryCount: document.querySelector("#memoryCount"),
  currentEmbeddingModel: document.querySelector("#currentEmbeddingModel"),
  memoryListBody: document.querySelector("#memoryListBody"),

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

// Bind Memory click handler
els.openMemory.addEventListener("click", () => {
  openMemoryExplorer();
});

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
  // Reset tabs on dialog open
  const tabListBtn = document.getElementById("modelsTabListBtn");
  const tabRolesBtn = document.getElementById("modelsTabRolesBtn");
  const tabListContent = document.getElementById("modelsTabListContent");
  const tabRolesContent = document.getElementById("modelsTabRolesContent");
  if (tabListBtn && tabRolesBtn && tabListContent && tabRolesContent) {
    tabListBtn.style.borderBottom = "2px solid var(--accent)";
    tabListBtn.style.color = "var(--accent)";
    tabRolesBtn.style.borderBottom = "2px solid transparent";
    tabRolesBtn.style.color = "var(--muted)";
    tabListContent.style.display = "block";
    tabRolesContent.style.display = "none";
  }
  renderModels();
  els.modelsDialog.showModal();
});

els.reloadModelsBtn.addEventListener("click", async () => {
  els.reloadModelsBtn.disabled = true;
  els.reloadSpinner.style.display = "inline-block";
  try {
    const response = await fetch("/api/models/reload", {
      method: "POST",
    });
    const data = await response.json();
    if (!response.ok) {
      addSystemMessage(`Reload error: ${data.error || "failed to reload models"}`);
      renderMessages();
      return;
    }
    state.models = data.models || [];
    syncActiveModel();
    updateOllamaStatus(true, data.ollama_version, data.from_cache);
    renderModels();
  } catch (err) {
    console.error("Reload models failed:", err);
    addSystemMessage(`Reload error: ${err.message || "failed to connect to server"}`);
    renderMessages();
  } finally {
    els.reloadSpinner.style.display = "none";
    els.reloadModelsBtn.disabled = false;
  }
});
els.openSettings.addEventListener("click", async () => {
  els.ollamaUrl.value = state.settings.ollama_base_url || "";
  els.ollamaProbeModels.value = state.settings.ollama_probe_models || "";
  els.ollamaImageSteps.value = state.settings.image_steps || 4;
  els.ollamaThinkToggle.checked = state.settings.ollama_think_enabled !== false;
  els.workspacePath.value = state.settings.workspace || "";
  els.sessionsPath.value = state.settings.sessions_path || "";
  els.memoryPath.value = state.settings.memory_path || "";
  els.skillsPath.value = state.settings.skills_path || "";
  els.webExposeToggle.checked = !!state.settings.server_expose_network;
  els.webAutoNameToggle.checked = state.settings.session_auto_name !== false;
  els.sessionExpiry.value = state.settings.telegram_session_expiry_min || 30;
  els.planConfirmationSelect.value = state.settings.plan_confirmation || "smart";
  els.telegramBotToken.value = state.settings.telegram_bot_token || "";
  els.telegramAuthorizedIds.value = state.settings.telegram_authorized_ids || "";
  els.telegramStartupNotification.checked = state.settings.telegram_startup_notification !== false;
  els.planConfirmationSelect.value = state.settings.plan_confirmation || "smart";
  els.serverEnabled.checked = state.settings.server_enabled !== false;
  els.serverPassword.value = state.settings.server_password || "";
  els.sleepModeToggle.checked = !!state.settings.sleep_mode_enabled;
  els.sleepModeContainer.style.display = state.settings.sleep_mode_enabled ? "block" : "none";
  els.sleepModeInactivity.value = state.settings.sleep_mode_inactivity_threshold || "30m";
  els.sleepModeResumeDelay.value = state.settings.sleep_mode_resume_delay || "10m";
  els.sleepModeSubagentsToggle.checked = !!state.settings.sleep_mode_subagents_enabled;

  const searchEnabled = !!state.settings.web_search_enabled;
  els.webSearchToggle.checked = searchEnabled;
  els.searchProvidersContainer.style.display = searchEnabled ? "block" : "none";

  const providersCsv = state.settings.web_search_priority || "";
  const parts = providersCsv.split(",").map(p => p.trim()).filter(Boolean);
  const activeMap = { brave: false, tavily: false, ddg: true };
  parts.forEach(p => {
    if (p === "brave" || p === "tavily" || p === "ddg") {
      activeMap[p] = true;
    }
  });

  const keysMap = {
    brave: state.settings.brave_search_api_key || "",
    tavily: state.settings.tavily_search_api_key || ""
  };

  let order = parts.filter(p => p === "brave" || p === "tavily");
  if (!order.includes("brave")) order.push("brave");
  if (!order.includes("tavily")) order.push("tavily");

  renderSearchProviders(order, activeMap, keysMap);

  els.webPort.value = state.settings.server_port || "8080";
  els.settingsDialog.showModal();
  // Request temporary microphone access to prompt permission dialog, so enumerateDevices gets actual labels
  if (navigator.mediaDevices && navigator.mediaDevices.getUserMedia) {
    try {
      const tempStream = await navigator.mediaDevices.getUserMedia({ audio: true });
      tempStream.getTracks().forEach(track => track.stop());
    } catch (e) {
      console.warn("Could not prompt mic permission on settings open:", e);
    }
  }
  await populateMicrophones();
});
els.settingsForm.addEventListener("submit", saveSettings);
if (els.webSearchToggle) {
  els.webSearchToggle.addEventListener("change", (e) => {
    if (els.searchProvidersContainer) {
      els.searchProvidersContainer.style.display = e.target.checked ? "block" : "none";
    }
  });
}
if (els.sleepModeToggle) {
  els.sleepModeToggle.addEventListener("change", (e) => {
    if (els.sleepModeContainer) {
      els.sleepModeContainer.style.display = e.target.checked ? "block" : "none";
    }
  });
}
els.form.addEventListener("submit", sendMessage);
els.imageInput.addEventListener("change", () => addFiles([...els.imageInput.files], "image"));
els.audioInput.addEventListener("change", () => addFiles([...els.audioInput.files], "audio"));
els.fileInput.addEventListener("change", () => addFileAttachments([...els.fileInput.files]));
els.recordControl.addEventListener("click", toggleRecording);
if (els.logoutBtn) {
  els.logoutBtn.addEventListener("click", () => {
    localStorage.removeItem("ollamabot.serverPassword");
    window.location.reload();
  });
}
if (els.skipBtn) {
  els.skipBtn.addEventListener("click", async () => {
    if (state.currentAbortController) {
      state.currentAbortController.abort();
    }
    if (state.activeSessionId) {
      try {
        await fetch(`/api/sessions/${encodeURIComponent(state.activeSessionId)}/abort`, { method: "POST" });
      } catch (err) {
        console.error("Failed to abort session on backend:", err);
      }
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

// Session filtering wiring
if (els.sessionSearch) {
  els.sessionSearch.addEventListener("input", (e) => {
    state.sessionSearchQuery = e.target.value;
    renderSessions();
  });
}

// Models dialog tabs switching wiring
const tabListBtn = document.getElementById("modelsTabListBtn");
const tabRolesBtn = document.getElementById("modelsTabRolesBtn");
const tabListContent = document.getElementById("modelsTabListContent");
const tabRolesContent = document.getElementById("modelsTabRolesContent");
if (tabListBtn && tabRolesBtn && tabListContent && tabRolesContent) {
  tabListBtn.addEventListener("click", () => {
    tabListBtn.style.borderBottom = "2px solid var(--accent)";
    tabListBtn.style.color = "var(--accent)";
    tabRolesBtn.style.borderBottom = "2px solid transparent";
    tabRolesBtn.style.color = "var(--muted)";
    tabListContent.style.display = "block";
    tabRolesContent.style.display = "none";
  });
  tabRolesBtn.addEventListener("click", () => {
    tabRolesBtn.style.borderBottom = "2px solid var(--accent)";
    tabRolesBtn.style.color = "var(--accent)";
    tabListBtn.style.borderBottom = "2px solid transparent";
    tabListBtn.style.color = "var(--muted)";
    tabListContent.style.display = "none";
    tabRolesContent.style.display = "block";
    renderRoleAssignments();
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
  const preview = e.target.closest(".media-preview.image, .generated-image[data-url]");
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

// --- Reusable confirm dialog (replaces native confirm()) ---
let _confirmResolve = null;

function showConfirm({ title = "Confirm", message = "", eyebrow = "Attention", okLabel = "Confirm", danger = true } = {}) {
  if (els.confirmTitle) els.confirmTitle.textContent = title;
  if (els.confirmEyebrow) els.confirmEyebrow.textContent = eyebrow;
  if (els.confirmMessage) els.confirmMessage.textContent = message;
  if (els.okConfirmBtn) {
    els.okConfirmBtn.textContent = okLabel;
    els.okConfirmBtn.className = danger ? "danger-button" : "primary-button";
  }
  els.confirmDialog.showModal();
  return new Promise((resolve) => {
    _confirmResolve = resolve;
  });
}

// --- Reusable toast notification (replaces native alert()) ---
let _toastTimer = null;
function showToast(message, type = "info", duration = 3500) {
  if (!els.toast) return;
  els.toast.textContent = message;
  els.toast.className = "toast" + (type === "error" ? " toast-error" : type === "success" ? " toast-success" : "");
  // force reflow so transition re-triggers on consecutive calls
  void els.toast.offsetWidth;
  els.toast.classList.add("visible");
  if (_toastTimer) clearTimeout(_toastTimer);
  _toastTimer = setTimeout(() => {
    els.toast.classList.remove("visible");
  }, duration);
}

// Confirm dialog wiring
if (els.cancelConfirmBtn) {
  els.cancelConfirmBtn.addEventListener("click", () => {
    els.confirmDialog.close();
    if (_confirmResolve) { _confirmResolve(false); _confirmResolve = null; }
    state.sessionIdToDelete = null;
  });
}

if (els.okConfirmBtn) {
  els.okConfirmBtn.addEventListener("click", () => {
    els.confirmDialog.close();
    if (_confirmResolve) { _confirmResolve(true); _confirmResolve = null; }
    // Legacy session-delete path (kept for direct session list usage)
    if (state.sessionIdToDelete) {
      deleteSession(state.sessionIdToDelete);
      state.sessionIdToDelete = null;
    }
  });
}

// Also resolve as cancelled when the dialog is closed via the × button or backdrop
els.confirmDialog.addEventListener("close", () => {
  if (_confirmResolve) { _confirmResolve(false); _confirmResolve = null; }
  state.sessionIdToDelete = null;
});

if (els.approveToolBtn) {
  els.approveToolBtn.addEventListener("click", () => {
    if (state.currentApprovalId) {
      respondToApproval(state.currentApprovalId, true);
    }
  });
}

if (els.denyToolBtn) {
  els.denyToolBtn.addEventListener("click", () => {
    if (state.currentApprovalId) {
      respondToApproval(state.currentApprovalId, false);
    }
  });
}

// Close on Escape key
els.imageDialog.addEventListener("keydown", (e) => {
  if (e.key === "Escape") {
    els.imageDialog.close();
  }
});

// Close dialogs on backdrop click (click outside dialog content)
function setupBackdropClose(dialog) {
  if (!dialog) return;
  dialog.addEventListener("click", (e) => {
    if (e.target !== dialog) return;
    const rect = dialog.getBoundingClientRect();
    const isInDialog = (
      rect.top <= e.clientY &&
      e.clientY <= rect.top + rect.height &&
      rect.left <= e.clientX &&
      e.clientX <= rect.left + rect.width
    );
    if (!isInDialog) {
      dialog.close();
    }
  });
}

setupBackdropClose(els.imageDialog);
setupBackdropClose(els.modelsDialog);
setupBackdropClose(els.settingsDialog);
setupBackdropClose(els.memoryDialog);
setupBackdropClose(els.memoryTextDialog);
setupBackdropClose(els.projectsDialog);


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

els.newSessionBtn.addEventListener("click", async () => {
  await waitForBootstrap();
  syncActiveModel();
  await createSession();
});

if (els.messages) {
  els.messages.addEventListener("click", async (e) => {
    const copyMsgBtn = e.target.closest(".message-copy-btn");
    if (copyMsgBtn) {
      const article = copyMsgBtn.closest(".message");
      if (!article) return;
      const idx = Array.from(els.messages.children).indexOf(article);
      if (idx === -1) return;
      const displayedMessages = groupMessagesAndTools(state.messages).filter(m => m.role !== "system");
      const msg = displayedMessages[idx];
      if (msg && msg.content) {
        try {
          await navigator.clipboard.writeText(msg.content);
          const originalText = copyMsgBtn.textContent;
          copyMsgBtn.textContent = "✅";
          copyMsgBtn.title = "Copied!";
          setTimeout(() => {
            copyMsgBtn.textContent = originalText;
            copyMsgBtn.title = "Copy raw markdown";
          }, 1500);
        } catch (err) {
          console.error("Failed to copy message:", err);
        }
      }
      return;
    }

    const copyCodeBtn = e.target.closest(".code-block-copy-btn");
    if (copyCodeBtn) {
      const wrapper = copyCodeBtn.closest(".code-block-wrapper");
      if (!wrapper) return;
      const codeEl = wrapper.querySelector("pre code");
      if (codeEl) {
        try {
          await navigator.clipboard.writeText(codeEl.textContent);
          const originalText = copyCodeBtn.textContent;
          copyCodeBtn.textContent = "Copied! ✅";
          setTimeout(() => {
            copyCodeBtn.textContent = originalText;
          }, 1500);
        } catch (err) {
          console.error("Failed to copy code block:", err);
        }
      }
      return;
    }

    // Smooth animated expand/collapse for thinking blocks
    const thinkingSummary = e.target.closest(".step-thinking summary");
    if (thinkingSummary) {
      const details = thinkingSummary.closest("details");
      if (!details) return;
      const pre = details.querySelector("pre");
      if (!pre) return;

      e.preventDefault();
      if (details.open) {
        // Closing
        pre.style.maxHeight = pre.scrollHeight + "px";
        pre.style.opacity = "1";
        requestAnimationFrame(() => {
          pre.style.transition = "max-height 0.3s ease, opacity 0.25s ease";
          pre.style.maxHeight = "0";
          pre.style.opacity = "0";
        });
        setTimeout(() => {
          details.open = false;
          pre.style.transition = "";
          pre.style.maxHeight = "";
          pre.style.opacity = "";
        }, 300);
      } else {
        // Opening
        details.open = true;
        pre.style.maxHeight = "0";
        pre.style.opacity = "0";
        requestAnimationFrame(() => {
          pre.style.transition = "max-height 0.3s ease, opacity 0.25s ease";
          pre.style.maxHeight = pre.scrollHeight + "px";
          pre.style.opacity = "1";
        });
        setTimeout(() => {
          pre.style.transition = "";
          pre.style.maxHeight = "";
          pre.style.opacity = "";
        }, 300);
      }
      return;
    }
  });
}
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
      showConfirm({
        title: "Delete Session?",
        message: "Are you sure you want to delete this chat session permanently? This action cannot be undone and all associated files will be deleted.",
        okLabel: "Delete Session",
        danger: true,
      }).then((confirmed) => {
        if (confirmed) deleteSession(id);
      });
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

let isCurrentlyConnected = true;

function startHealthCheck() {
  setInterval(async () => {
    try {
      const response = await fetch("/api/health");
      if (response.ok) {
        const data = await response.json();
        updateOllamaStatus(true, data.ollama_version);
        if (!isCurrentlyConnected) {
          isCurrentlyConnected = true;
          loadModels();
        }
      } else {
        updateOllamaStatus(false);
        isCurrentlyConnected = false;
      }
    } catch (err) {
      updateOllamaStatus(false);
      isCurrentlyConnected = false;
    }
  }, 5000);
}

function startSessionPolling() {
  setInterval(async () => {
    if (!isCurrentlyConnected) return;

    try {
      // 1. Poll sessions list
      const sessResp = await fetch("/api/sessions");
      if (sessResp.ok) {
        const fetchedSessions = (await sessResp.json()) || [];
        const currentSessions = state.sessions || [];
        
        // Check if session list changed (compare length, IDs, titles, or updated_at)
        let changed = fetchedSessions.length !== currentSessions.length;
        if (!changed) {
          for (let i = 0; i < fetchedSessions.length; i++) {
            if (fetchedSessions[i].id !== currentSessions[i].id || 
                fetchedSessions[i].updated_at !== currentSessions[i].updated_at ||
                fetchedSessions[i].title !== currentSessions[i].title) {
              changed = true;
              break;
            }
          }
        }
        
        const oldIds = new Set(currentSessions.map(s => s.id));
        const newSessions = fetchedSessions.filter(s => !oldIds.has(s.id));
        
        if (changed) {
          // Preserve the active session if it's not in the server list yet
          if (state.activeSessionId && !fetchedSessions.some((s) => s.id === state.activeSessionId)) {
            const localCopy = (state.sessions || []).find((s) => s.id === state.activeSessionId);
            const stub = localCopy || { id: state.activeSessionId, title: "New session", updated_at: new Date().toISOString() };
            state.sessions = [stub, ...fetchedSessions.filter((s) => s.id !== state.activeSessionId)];
          } else {
            state.sessions = fetchedSessions;
          }
          renderSessions();
          updateComposerUI();
        }

        if (newSessions.length > 0 && !state.isProcessing) {
          // Switch to the newest session if it was created externally (e.g. by Telegram)
          const newest = fetchedSessions[0];
          if (newest && newest.id !== state.activeSessionId) {
            console.log("[Session Polling] Switching to newly created session:", newest.id);
            await loadSession(newest.id);
          }
        }
      }

      // 2. Poll active session messages
      if (state.activeSessionId && !state.isProcessing) {
        const activeSessResp = await fetch(`/api/sessions/${encodeURIComponent(state.activeSessionId)}`);
        if (activeSessResp.ok) {
          const activeSess = await activeSessResp.json();
          const rawMsgs = activeSess.messages || [];
          const normalized = normalizeRawMessages(rawMsgs);
          
          // Compare with state.messages length or content
          let msgsChanged = normalized.length !== state.messages.length;
          if (!msgsChanged) {
            for (let i = 0; i < normalized.length; i++) {
              const msg = normalized[i];
              const stateMsg = state.messages[i];
              if (!stateMsg || 
                  msg.role !== stateMsg.role || 
                  msg.content !== stateMsg.content || 
                  JSON.stringify(msg.steps) !== JSON.stringify(stateMsg.steps)) {
                msgsChanged = true;
                break;
              }
            }
          }
          
          if (msgsChanged) {
            state.messages = normalized;
            renderMessages();
            renderAttachments();
            updateContextBar();
          }
        }
      }
    } catch (err) {
      console.warn("Session polling failed:", err);
    }
  }, 10000);
}

let eventSource = null;
let sessionUpdateDebounce = null;

function startRealtimeEvents() {
  if (eventSource) {
    eventSource.close();
  }

  const serverPassword = localStorage.getItem("ollamabot.serverPassword") || "";
  const queryParam = serverPassword ? `?password=${encodeURIComponent(serverPassword)}` : "";

  eventSource = new EventSource(`/api/events${queryParam}`);

  eventSource.addEventListener("session_updated", async (event) => {
    const sessionID = event.data;
    console.log("[Events Hub] Session updated:", sessionID);

    if (sessionUpdateDebounce) {
      clearTimeout(sessionUpdateDebounce);
    }
    sessionUpdateDebounce = setTimeout(async () => {
      sessionUpdateDebounce = null;
      // 1. Refresh session list
      await loadSessions();

      // 2. If it's the active session, reload it
      if (state.activeSessionId === sessionID) {
        if (!state.isProcessing) {
          await loadSession(sessionID);
          updateComposerUI();
        }
      }
    }, 800);
  });

  eventSource.onerror = (err) => {
    console.warn("[Events Hub] EventSource connection error, reconnecting in 5s...", err);
    eventSource.close();
    setTimeout(startRealtimeEvents, 5000);
  };
}

bootstrap();
updateComposerUI();

async function bootstrap() {
  bootstrapPromise = (async () => {
    await loadSettings();
    await loadModels();
    applySidebarState();
    requestAnimationFrame(() => document.body.classList.remove("first-load"));
    await loadSessions();

    // Ensure at least one session exists - create one if needed
    await ensureActiveSession();

    startHealthCheck();
    startRealtimeEvents();
    startSessionPolling();
    state.bootstrapReady = true;
    updateComposerUI();
  })();
  await bootstrapPromise;
}

// Ensures there's always an active session, creating one if necessary
async function ensureActiveSession() {
  // Check if the current active session exists in the loaded list
  const sessionExists = state.sessions.some((s) => s.id === state.activeSessionId);
  
  if (state.activeSessionId && sessionExists) {
    // Valid active session - load it
    await loadSession(state.activeSessionId);
    return;
  }
  
  // No valid active session - clear stale data
  if (state.activeSessionId && !sessionExists) {
    state.activeSessionId = null;
    localStorage.removeItem("ollamabot.activeSessionId");
  }
  
  // Try to create a server session
  const success = await createSession();
  if (success) {
    // Server session created and activated - we're done
    return;
  }
  
  // Server session creation failed - create client-side session
  // This ensures the UI always has a session to work with
  await createClientSession();

  // Final safety net: if somehow we still have no active session, create one more time
  if (!state.activeSessionId) {
    await createClientSession();
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

// Dynamic render function for search providers list in priority order
function renderSearchProviders(providersOrder, activeMap, keysMap) {
  const list = els.searchProvidersList;
  if (!list) return;
  list.innerHTML = "";

  // Normalize order: ensure only brave, tavily and ddg. Brave and Tavily are reorderable.
  let order = providersOrder.filter(id => id === "brave" || id === "tavily");
  if (!order.includes("brave")) order.push("brave");
  if (!order.includes("tavily")) order.push("tavily");
  order.push("ddg"); // ddg is always last and always active

  order.forEach((id, index) => {
    const isActive = id === "ddg" ? true : (activeMap[id] !== false);
    const isDDG = id === "ddg";
    const keyVal = keysMap[id] || "";
    
    let statusClass = "disabled";
    let statusText = "Disabled";
    if (isDDG) {
      statusClass = "always-active";
      statusText = "Always Active";
    } else if (isActive) {
      if (keyVal && keyVal !== "") {
        statusClass = "configured";
        statusText = "Configured";
      } else {
        statusClass = "missing";
        statusText = "No API Key";
      }
    }

    const card = document.createElement("div");
    card.className = `provider-card ${(!isActive && !isDDG) ? "card-disabled" : ""}`;
    card.dataset.id = id;

    let headerHtml = `
      <div class="provider-header">
        <div class="provider-title-container">
          ${isDDG ? "" : `<input type="checkbox" id="provider_active_${id}" ${isActive ? "checked" : ""} />`}
          <span class="provider-title">${id === "brave" ? "Brave Search" : id === "tavily" ? "Tavily Search" : "DuckDuckGo"}</span>
          <span class="provider-status-badge ${statusClass}">${statusText}</span>
        </div>
    `;

    if (!isDDG) {
      const isFirst = index === 0;
      const isLast = index === 1; // brave & tavily are at indices 0 and 1
      headerHtml += `
        <div class="provider-drag-priority">
          <button type="button" class="priority-btn move-up" ${isFirst ? "disabled" : ""} title="Move Up">▲</button>
          <button type="button" class="priority-btn move-down" ${isLast ? "disabled" : ""} title="Move Down">▼</button>
        </div>
      `;
    }
    headerHtml += `</div>`;

    let bodyHtml = "";
    if (id === "brave") {
      bodyHtml = `
        <div class="provider-body" style="${isActive ? "" : "display:none;"}">
          <label class="provider-api-key-label">
            Brave Search API Key
            <div class="password-input-wrapper" style="position: relative; display: flex; align-items: center; width: 100%;">
              <input type="password" class="provider-api-key-input" id="brave_api_key_input" value="${escapeHtml(keyVal)}" placeholder="Enter your Brave Search API key..." autocomplete="off" style="width: 100%; padding-right: 40px;" />
              <button type="button" class="password-toggle-btn" style="position: absolute; right: 8px; background: none; border: none; color: var(--muted); cursor: pointer; font-size: 16px; display: flex; align-items: center; justify-content: center; height: 100%; width: 32px; padding: 0; outline: none; user-select: none;" title="Toggle visibility">👁️</button>
            </div>
          </label>
          <details class="provider-howto">
            <summary>📖 How to get a Brave Search API key</summary>
            <div class="provider-howto-body">
              <p>Brave Search API costs <strong>$5.00 / 1,000 requests</strong>, but automatically includes <strong>$5 in free credits every month</strong> — roughly <strong>~1,000 free searches/month</strong>. A credit card is required to activate the account.</p>
              <ol>
                <li>Go to <a href="https://api.search.brave.com/" target="_blank" rel="noopener">api.search.brave.com</a> and click <strong>"Get started"</strong>.</li>
                <li>Create an account and add a payment method (needed even for the free credits).</li>
                <li>Go to <strong>Subscriptions → Available plans → Search</strong> and subscribe.</li>
                <li>Go to <strong>API keys</strong>, create a new key, copy it, and paste it above.</li>
              </ol>
              <p class="provider-note">💡 The key is stored in your <code>.env</code> file.</p>
            </div>
          </details>
        </div>
      `;
    } else if (id === "tavily") {
      bodyHtml = `
        <div class="provider-body" style="${isActive ? "" : "display:none;"}">
          <label class="provider-api-key-label">
            Tavily Search API Key
            <div class="password-input-wrapper" style="position: relative; display: flex; align-items: center; width: 100%;">
              <input type="password" class="provider-api-key-input" id="tavily_api_key_input" value="${escapeHtml(keyVal)}" placeholder="Enter your Tavily API key..." autocomplete="off" style="width: 100%; padding-right: 40px;" />
              <button type="button" class="password-toggle-btn" style="position: absolute; right: 8px; background: none; border: none; color: var(--muted); cursor: pointer; font-size: 16px; display: flex; align-items: center; justify-content: center; height: 100%; width: 32px; padding: 0; outline: none; user-select: none;" title="Toggle visibility">👁️</button>
            </div>
          </label>
          <details class="provider-howto">
            <summary>📖 How to get a Tavily API key</summary>
            <div class="provider-howto-body">
              <p>Tavily Search API offers a <strong>100% Free Plan</strong> (no credit card required) that includes <strong>1,000 free searches per month</strong>. It is designed specifically for LLM agents.</p>
              <ol>
                <li>Go to <a href="https://tavily.com/" target="_blank" rel="noopener">tavily.com</a> and click <strong>"Sign up"</strong>.</li>
                <li>Create your account.</li>
                <li>From your dashboard, copy your API key and paste it above.</li>
              </ol>
              <p class="provider-note">💡 Tavily is highly recommended for search accuracy in agents. The key is stored in your <code>.env</code> file.</p>
            </div>
          </details>
        </div>
      `;
    } else if (id === "ddg") {
      bodyHtml = `
        <div class="provider-body">
          <p class="provider-note" style="margin: 0;">DuckDuckGo handles free web scraping as a fallback option when active. No API keys or external accounts are required.</p>
        </div>
      `;
    }

    card.innerHTML = headerHtml + bodyHtml;
    list.appendChild(card);

    if (!isDDG) {
      const checkbox = card.querySelector(`#provider_active_${id}`);
      checkbox.addEventListener("change", (e) => {
        const checked = e.target.checked;
        const body = card.querySelector(".provider-body");
        if (body) body.style.display = checked ? "" : "none";
        if (checked) {
          card.classList.remove("card-disabled");
        } else {
          card.classList.add("card-disabled");
        }
        updateBadgeStatus(id, card);
      });

      const keyInput = card.querySelector(`.provider-api-key-input`);
      if (keyInput) {
        keyInput.addEventListener("input", () => {
          updateBadgeStatus(id, card);
        });
      }

      const upBtn = card.querySelector(".move-up");
      const downBtn = card.querySelector(".move-down");
      if (upBtn) {
        upBtn.addEventListener("click", () => {
          const currentKeys = getKeysFromDOM();
          const currentActives = getActivesFromDOM();
          const currentOrder = getOrderFromDOM();
          const itemIdx = currentOrder.indexOf(id);
          if (itemIdx > 0) {
            const temp = currentOrder[itemIdx - 1];
            currentOrder[itemIdx - 1] = currentOrder[itemIdx];
            currentOrder[itemIdx] = temp;
            renderSearchProviders(currentOrder, currentActives, currentKeys);
          }
        });
      }
      if (downBtn) {
        downBtn.addEventListener("click", () => {
          const currentKeys = getKeysFromDOM();
          const currentActives = getActivesFromDOM();
          const currentOrder = getOrderFromDOM();
          const itemIdx = currentOrder.indexOf(id);
          if (itemIdx < currentOrder.length - 2) {
            const temp = currentOrder[itemIdx + 1];
            currentOrder[itemIdx + 1] = currentOrder[itemIdx];
            currentOrder[itemIdx] = temp;
            renderSearchProviders(currentOrder, currentActives, currentKeys);
          }
        });
      }
    }
  });
}

function updateBadgeStatus(id, card) {
  const checkbox = card.querySelector(`#provider_active_${id}`);
  const keyInput = card.querySelector(`.provider-api-key-input`);
  const badge = card.querySelector(`.provider-status-badge`);
  if (!badge) return;

  const isActive = checkbox ? checkbox.checked : false;
  const keyVal = keyInput ? keyInput.value.trim() : "";

  badge.className = "provider-status-badge";
  if (!isActive) {
    badge.classList.add("disabled");
    badge.textContent = "Disabled";
  } else if (keyVal && keyVal !== "") {
    badge.classList.add("configured");
    badge.textContent = "Configured";
  } else {
    badge.classList.add("missing");
    badge.textContent = "No API Key";
  }
}

function getOrderFromDOM() {
  if (!els.searchProvidersList) return ["brave", "tavily", "ddg"];
  const cards = els.searchProvidersList.querySelectorAll(".provider-card");
  return Array.from(cards).map(card => card.dataset.id);
}

function getActivesFromDOM() {
  const actives = {};
  if (!els.searchProvidersList) return { brave: false, tavily: false, ddg: true };
  const cards = els.searchProvidersList.querySelectorAll(".provider-card");
  cards.forEach(card => {
    const id = card.dataset.id;
    if (id === "ddg") {
      actives[id] = true;
    } else {
      const checkbox = card.querySelector(`#provider_active_${id}`);
      actives[id] = checkbox ? checkbox.checked : false;
    }
  });
  return actives;
}

function getKeysFromDOM() {
  const keys = {};
  if (!els.searchProvidersList) return { brave: "", tavily: "" };
  const cards = els.searchProvidersList.querySelectorAll(".provider-card");
  cards.forEach(card => {
    const id = card.dataset.id;
    const input = card.querySelector(`.provider-api-key-input`);
    keys[id] = input ? input.value : "";
  });
  return keys;
}

async function loadSettings() {
  const response = await fetch("/api/settings");
  if (!response.ok) return;
  state.settings = await response.json();
  els.ollamaUrl.value = state.settings.ollama_base_url || "";
  els.ollamaProbeModels.value = state.settings.ollama_probe_models || "";
  els.ollamaImageSteps.value = state.settings.image_steps || 4;
  els.ollamaThinkToggle.checked = state.settings.ollama_think_enabled !== false;
  els.workspacePath.value = state.settings.workspace || "";
  els.sessionsPath.value = state.settings.sessions_path || "";
  els.memoryPath.value = state.settings.memory_path || "";
  els.skillsPath.value = state.settings.skills_path || "";
  els.webExposeToggle.checked = !!state.settings.server_expose_network;
  els.webAutoNameToggle.checked = state.settings.session_auto_name !== false;
  els.sessionExpiry.value = state.settings.telegram_session_expiry_min || 30;
  els.telegramBotToken.value = state.settings.telegram_bot_token || "";
  els.telegramAuthorizedIds.value = state.settings.telegram_authorized_ids || "";
  els.telegramStartupNotification.checked = state.settings.telegram_startup_notification !== false;
  els.planConfirmationSelect.value = state.settings.plan_confirmation || "smart";

  const searchEnabled = !!state.settings.web_search_enabled;
  els.webSearchToggle.checked = searchEnabled;
  els.searchProvidersContainer.style.display = searchEnabled ? "block" : "none";

  const providersCsv = state.settings.web_search_priority || "";
  const parts = providersCsv.split(",").map(p => p.trim()).filter(Boolean);
  const activeMap = { brave: false, tavily: false, ddg: true };
  parts.forEach(p => {
    if (p === "brave" || p === "tavily" || p === "ddg") {
      activeMap[p] = true;
    }
  });

  const keysMap = {
    brave: state.settings.brave_search_api_key || "",
    tavily: state.settings.tavily_search_api_key || ""
  };

  let order = parts.filter(p => p === "brave" || p === "tavily");
  if (!order.includes("brave")) order.push("brave");
  if (!order.includes("tavily")) order.push("tavily");

  renderSearchProviders(order, activeMap, keysMap);

  els.webPort.value = state.settings.server_port || "8080";
  els.serverEnabled.checked = state.settings.server_enabled !== false;
  els.serverPassword.value = state.settings.server_password || "";
  if (state.settings.server_password) {
    if (els.logoutBtn) els.logoutBtn.style.display = "inline-block";
  } else {
    if (els.logoutBtn) els.logoutBtn.style.display = "none";
  }
  els.sleepModeToggle.checked = !!state.settings.sleep_mode_enabled;
  els.sleepModeContainer.style.display = state.settings.sleep_mode_enabled ? "block" : "none";
  els.sleepModeInactivity.value = state.settings.sleep_mode_inactivity_threshold || "30m";
  els.sleepModeResumeDelay.value = state.settings.sleep_mode_resume_delay || "10m";
  els.sleepModeSubagentsToggle.checked = !!state.settings.sleep_mode_subagents_enabled;
  
  state.subagentModel = state.settings.model_subagent || "";
  localStorage.setItem("ollamabot.subagentModel", state.subagentModel);
  
  state.learningModel = state.settings.model_learning || "";
  localStorage.setItem("ollamabot.learningModel", state.learningModel);

  if (state.settings.model_default) {
    state.activeModel = state.settings.model_default;
  }
  if (state.settings.model_vision) state.visionModel = state.settings.model_vision;
  if (state.settings.model_audio) state.audioModel = state.settings.model_audio;
  if (state.settings.model_embeddings) state.embeddingsModel = state.settings.model_embeddings;
  if (state.settings.model_image) state.imageModel = state.settings.model_image;
  if (state.settings.image_steps) state.imageSteps = state.settings.image_steps;
  localStorage.setItem("ollamabot.visionModel", state.visionModel);
  localStorage.setItem("ollamabot.audioModel", state.audioModel);
  localStorage.setItem("ollamabot.embeddingsModel", state.embeddingsModel);
  localStorage.setItem("ollamabot.imageModel", state.imageModel);
  localStorage.setItem("ollamabot.imageSteps", state.imageSteps);
}

async function saveSettings(event) {
  event.preventDefault();
  state.selectedMicId = els.micSelect.value;
  localStorage.setItem("ollamabot.selectedMicId", state.selectedMicId);

  const webSearchEnabled = els.webSearchToggle.checked;
  const providersOrder = getOrderFromDOM();
  const providersActive = getActivesFromDOM();
  const keys = getKeysFromDOM();

  const activeProvidersList = providersOrder.filter(id => providersActive[id]);
  const searchProvidersCsv = webSearchEnabled
    ? (activeProvidersList.length > 0 ? activeProvidersList.join(",") : "ddg")
    : "none";

  const braveActive = providersActive["brave"] && webSearchEnabled;
  const tavilyActive = providersActive["tavily"] && webSearchEnabled;

  const braveKey = braveActive ? (keys.brave ? keys.brave.trim() : "") : (state.settings.brave_search_api_key || "");
  const tavilyKey = tavilyActive ? (keys.tavily ? keys.tavily.trim() : "") : (state.settings.tavily_search_api_key || "");

  const response = await fetch("/api/settings", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      ollama_base_url: els.ollamaUrl.value.trim(),
      ollama_probe_models: els.ollamaProbeModels.value.trim(),
      workspace: els.workspacePath.value.trim(),
      sessions_path: els.sessionsPath.value.trim(),
      memory_path: els.memoryPath.value.trim(),
      skills_path: els.skillsPath.value.trim(),
      model_default: state.activeModel,
      model_vision: state.visionModel,
      model_audio: state.audioModel,
      model_embeddings: state.embeddingsModel,
      model_image: state.imageModel,
      image_steps: parseInt(els.ollamaImageSteps.value.trim(), 10) || 4,
      ollama_think_enabled: els.ollamaThinkToggle.checked,
      web_search_enabled: webSearchEnabled,
      web_search_priority: searchProvidersCsv,
      brave_search_api_key: braveKey,
      tavily_search_api_key: tavilyKey,
      server_enabled: els.serverEnabled.checked,
      server_expose_network: els.webExposeToggle.checked,
      session_auto_name: els.webAutoNameToggle.checked,
      telegram_session_expiry_min: parseInt(els.sessionExpiry.value.trim(), 10) || 30,
      telegram_bot_token: els.telegramBotToken.value.trim(),
      telegram_authorized_ids: els.telegramAuthorizedIds.value.trim(),
      telegram_startup_notification: els.telegramStartupNotification.checked,
      plan_confirmation: els.planConfirmationSelect.value,
      server_port: els.webPort.value.trim() || "8080",
      sleep_mode_enabled: els.sleepModeToggle.checked,
      sleep_mode_inactivity_threshold: els.sleepModeInactivity.value.trim(),
      sleep_mode_resume_delay: els.sleepModeResumeDelay.value.trim(),
      sleep_mode_subagents_enabled: els.sleepModeSubagentsToggle.checked,
      model_subagent: state.subagentModel,
      model_learning: state.learningModel,
      server_password: els.serverPassword.value.trim(),
    }),
  });
  const data = await response.json();
  if (!response.ok) {
    addSystemMessage(`Settings error: ${data.error || "could not save settings"}`);
    return;
  }
  const newPass = els.serverPassword.value.trim();
  if (newPass && newPass !== "***") {
    localStorage.setItem("ollamabot.serverPassword", newPass);
  } else if (!newPass) {
    localStorage.removeItem("ollamabot.serverPassword");
  }
  state.settings = data;
  state.activeModel = data.model_default || "";
  state.visionModel = data.model_vision || "";
  state.audioModel = data.model_audio || "";
  state.embeddingsModel = data.model_embeddings || "";
  state.imageModel = data.model_image || "";
  state.imageSteps = data.image_steps || 4;
  state.learningModel = data.model_learning || "";
  state.subagentModel = data.model_subagent || "";

  localStorage.setItem("ollamabot.visionModel", state.visionModel);
  localStorage.setItem("ollamabot.audioModel", state.audioModel);
  localStorage.setItem("ollamabot.embeddingsModel", state.embeddingsModel);
  localStorage.setItem("ollamabot.imageModel", state.imageModel);
  localStorage.setItem("ollamabot.imageSteps", state.imageSteps);
  localStorage.setItem("ollamabot.learningModel", state.learningModel);
  localStorage.setItem("ollamabot.subagentModel", state.subagentModel);

  els.settingsDialog.close();
  await loadModels();
}

async function saveRoleModels() {
  const oldModel = state.settings.model_embeddings || "";
  const newModel = state.embeddingsModel || "";
  const changed = oldModel !== newModel;

  const response = await fetch("/api/settings", {
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
      model_image: state.imageModel,
      image_steps: state.imageSteps || 4,
      ollama_think_enabled: state.settings.ollama_think_enabled !== false,
      web_search_enabled: state.settings.web_search_enabled || false,
      web_search_priority: state.settings.web_search_priority || "ddg",
      brave_search_api_key: state.settings.brave_search_api_key || "",
      tavily_search_api_key: state.settings.tavily_search_api_key || "",
      server_expose_network: state.settings.server_expose_network || false,
      server_enabled: state.settings.server_enabled !== false,
      session_auto_name: state.settings.session_auto_name !== false,
      server_port: state.settings.server_port || "8080",
      sleep_mode_enabled: state.settings.sleep_mode_enabled || false,
      sleep_mode_inactivity_threshold: state.settings.sleep_mode_inactivity_threshold || "30m",
      sleep_mode_resume_delay: state.settings.sleep_mode_resume_delay || "10m",
      sleep_mode_subagents_enabled: state.settings.sleep_mode_subagents_enabled || false,
      model_subagent: state.subagentModel,
      model_learning: state.learningModel,
      server_password: state.settings.server_password || "",
      telegram_session_expiry_min: state.settings.telegram_session_expiry_min || 30,
      telegram_bot_token: state.settings.telegram_bot_token || "",
      telegram_authorized_ids: state.settings.telegram_authorized_ids || "",
      telegram_startup_notification: state.settings.telegram_startup_notification !== false,
      plan_confirmation: state.settings.plan_confirmation || "smart",
    }),
  });

  if (response.ok) {
    state.settings = await response.json();
    if (changed && newModel) {
      setTimeout(() => {
        showConfirm({
          title: "Re-index Memory?",
          message: "The embedding model has changed. This can make existing memory entries unsearchable or inaccurate. It is highly recommended to re-index all memory entries now. Would you like to open the Memory Explorer to do so?",
          okLabel: "Open Memory Explorer",
          danger: false,
        }).then((confirmed) => {
          if (!confirmed) return;
          // Close models dialog first if open
          els.modelsDialog.close();
          openMemoryExplorer();
        });
      }, 300);
    }
  }
}

function updateOllamaStatus(connected, version, fromCache) {
  if (connected) {
    els.baseUrl.textContent = `Ollama v${version || "unknown"}`;
    els.baseUrl.style.borderColor = "var(--accent)";
    els.baseUrl.style.color = "var(--accent)";
    
    // VRAM loaded models
    const loaded = state.models.filter((m) => m.loaded);
    const vram = loaded.reduce((sum, model) => sum + (model.size_vram || 0), 0);
    if (loaded.length > 0) {
      const names = loaded.map(m => m.name).join(", ");
      els.memoryState.textContent = `VRAM: ${formatBytes(vram)} (${names})`;
    } else {
      els.memoryState.textContent = `VRAM: - (0 models)`;
    }
  } else {
    els.baseUrl.textContent = `Ollama: Offline`;
    els.baseUrl.style.borderColor = "var(--bad)";
    els.baseUrl.style.color = "var(--bad)";
    els.memoryState.textContent = `VRAM: -`;
    if (els.cacheState && !state.isProcessing) {
      els.cacheState.textContent = `offline`;
      els.cacheState.style.borderColor = "var(--bad)";
      els.cacheState.style.color = "var(--bad)";
    }
  }
}

async function loadModels() {
  els.modelsBody.innerHTML = `<div class="empty">Loading models...</div>`;
  try {
    const response = await fetch("/api/models");
    const data = await response.json();
    if (!response.ok) {
      throw new Error(data.error || "Failed to load models");
    }
    state.models = data.models || [];
    syncActiveModel();
    updateOllamaStatus(true, data.ollama_version, data.from_cache);
    renderModels();
  } catch (err) {
    els.modelsBody.innerHTML = `<div class="empty">${escapeHtml(err.message || err)}</div>`;
    updateOllamaStatus(false);
  }
}

function syncActiveModel() {
  const fromSettings = state.settings?.model_default;
  const defaultFromList = state.models.find((m) => m.is_default && canBeMain(m))?.name;

  if (fromSettings) {
    state.activeModel = fromSettings;
  } else if (defaultFromList) {
    state.activeModel = defaultFromList;
  }

  if (!state.activeModel || !state.models.some((m) => m.name === state.activeModel)) {
    const preferred = state.models.find((m) => m.is_default && canBeMain(m))
      || (fromSettings ? state.models.find((m) => m.name === fromSettings && canBeMain(m)) : null)
      || state.models.find((m) => canBeMain(m));
    state.activeModel = preferred?.name || state.activeModel || "";
  }

  renderActive();
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
    { key: "learningModel", label: "learn" },
    { key: "subagentModel", label: "subagent" },
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
  const canImage = modelForRole("vision") !== null;
  const canAudio = modelForRole("audio") !== null;
  els.imageControl.hidden = !canImage;
  els.audioControl.hidden = !canAudio;
  els.recordControl.hidden = !canAudio;
  state.attachments = state.attachments.filter((attachment) => attachment.kind === "file" || capabilityFor(attachment.kind));
  renderAttachments();
}

// Returns true if a model meets the minimum requirements for the main role.
function canBeMain(model) {
  const caps = model?.capabilities || {};
  return caps.completion === "comprobado" && caps.tools === "comprobado";
}

// Returns the model name that handles a given role, or null if unavailable.
// Priority: dedicated role model (if capable) → main model (if capable) → null.
function modelForRole(role) {
  const capKey = role === "vision" ? "vision" : "audio";
  const dedicated = role === "vision" ? state.visionModel : state.audioModel;
  if (dedicated) {
    const model = state.models.find((m) => m.name === dedicated);
    // Trust unknown (unprobed) models; reject probed models lacking the capability.
    if (!model) return dedicated;
    const status = model.capabilities?.[capKey];
    if (status === "comprobado" || status === "inferido") return dedicated;
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
  } else if (filter === "subagent") {
    filteredModels = filteredModels.filter((m) => canBeMain(m));
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
    const canImage = model.capabilities?.image === "comprobado" || model.capabilities?.image === "inferido";

    const isMain = model.name === state.activeModel;
    const isLearning = model.name === state.learningModel;
    const isSubagent = model.name === state.subagentModel;
    const isVision = model.name === state.visionModel || (isMain && !state.visionModel && canVision);
    const isAudio = model.name === state.audioModel || (isMain && !state.audioModel && canAudio);
    const isEmbed = model.name === state.embeddingsModel;
    const isImage = model.name === state.imageModel;
    const isUseless = !isMainCapable && !canVision && !canAudio && !canEmbed && !canImage && !isLearning && !isSubagent;

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
    if (isLearning) activeRolesHtml += `<span class="active-role-pill learning" title="This model is assigned to the LEARNING role">Learn</span>`;
    if (isSubagent) activeRolesHtml += `<span class="active-role-pill subagent" title="This model is assigned to the SUBAGENT role" style="background:#ff6ebd;color:#180a13;box-shadow:0 0 8px rgba(255,110,189,0.4);">Subagent</span>`;
    if (isVision) activeRolesHtml += `<span class="active-role-pill vision" title="This model is assigned to the VISION role">Vision</span>`;
    if (isAudio) activeRolesHtml += `<span class="active-role-pill audio" title="This model is assigned to the AUDIO role">Audio</span>`;
    if (isEmbed) activeRolesHtml += `<span class="active-role-pill embed" title="This model is assigned to the EMBEDDINGS role">Embed</span>`;
    if (isImage) activeRolesHtml += `<span class="active-role-pill image" title="This model is assigned to the IMAGE GENERATION role" style="background:#ff9f40;color:#180a13;box-shadow:0 0 8px rgba(255,159,64,0.4);">Image</span>`;
    const activeRolesContainer = activeRolesHtml ? `<div class="active-roles-container">${activeRolesHtml}</div>` : "";

    const statusBadgeHtml = model.loaded ?
      `<span class="model-loaded-badge"><span class="pulse-dot"></span>loaded</span>` :
      `<span class="model-offline-badge">offline</span>`;

    let roleButtonsHtml = "";
    if (isMainCapable) {
      roleButtonsHtml += `<button class="choose role-btn ${isMain ? "active" : ""}" data-role="main" data-model="${escapeAttr(model.name)}">⚡ Main</button>`;
      roleButtonsHtml += `<button class="choose role-btn ${isLearning ? "active" : ""}" data-role="learning" data-model="${escapeAttr(model.name)}">🎓 Learn</button>`;
      roleButtonsHtml += `<button class="choose role-btn ${isSubagent ? "active" : ""}" data-role="subagent" data-model="${escapeAttr(model.name)}">🤖 Subagent</button>`;
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
    if (canImage) {
      roleButtonsHtml += `<button class="choose role-btn ${isImage ? "active" : ""}" data-role="image" data-model="${escapeAttr(model.name)}">🎨 Image</button>`;
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
    button.addEventListener("click", async () => {
      const role = button.dataset.role;
      const model = button.dataset.model;
      if (role === "main") {
        state.activeModel = model;
        await saveRoleModels();
        renderActive();
        renderModels();
      } else {
        const stateKey = role === "vision" ? "visionModel" : role === "audio" ? "audioModel" : role === "learning" ? "learningModel" : role === "subagent" ? "subagentModel" : role === "image" ? "imageModel" : "embeddingsModel";
        const lsKey = role === "vision" ? "ollamabot.visionModel" : role === "audio" ? "ollamabot.audioModel" : role === "learning" ? "ollamabot.learningModel" : role === "subagent" ? "ollamabot.subagentModel" : role === "image" ? "ollamabot.imageModel" : "ollamabot.embeddingsModel";
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

function renderRoleAssignments() {
  const container = document.getElementById("rolesListBody");
  if (!container) return;
  container.innerHTML = "";

  const roles = [
    {
      id: "main",
      name: "⚡ Main",
      desc: "Handles chat, session name generation, and tool execution. Requires TEXT + TOOLS capability.",
      value: state.activeModel,
      required: true,
      fallback: ""
    },
    {
      id: "learning",
      name: "🎓 Learn",
      desc: "Reflects on chat history to refine skills during sleep mode. Requires TEXT + TOOLS. Fallback: Main.",
      value: state.learningModel,
      required: false,
      fallback: "Main model"
    },
    {
      id: "subagent",
      name: "🤖 Subagent",
      desc: "Dedicated model used for background tasks and automated execution. Requires TEXT + TOOLS. Fallback: Main.",
      value: state.subagentModel,
      required: false,
      fallback: "Main model"
    },
    {
      id: "vision",
      name: "👁️ Vision",
      desc: "Processes image attachments. Requires VISION capability. Fallback: Main (if capable).",
      value: state.visionModel,
      required: false,
      fallback: "Main model (if capable)"
    },
    {
      id: "audio",
      name: "🔊 Audio",
      desc: "Processes voice recordings and audio files. Requires AUDIO capability. Fallback: Main (if capable).",
      value: state.audioModel,
      required: false,
      fallback: "Main model (if capable)"
    },
    {
      id: "embeddings",
      name: "🔗 Embed",
      desc: "Used for semantic search, memory vectorization, and indexing. Requires EMBED capability.",
      value: state.embeddingsModel,
      required: false,
      fallback: "disabled"
    },
    {
      id: "image",
      name: "🎨 Image",
      desc: "Generates images using diffusion models (Flux, etc.). Requires IMAGE capability.",
      value: state.imageModel,
      required: false,
      fallback: "disabled"
    }
  ];

  for (const r of roles) {
    const card = document.createElement("div");
    card.className = "role-assignment-card";

    let badgeHtml = "";
    if (r.value) {
      badgeHtml = `<span class="role-assignment-badge assigned">${escapeHtml(r.value)}</span>`;
    } else {
      badgeHtml = `<span class="role-assignment-badge fallback">${escapeHtml(r.required ? "None selected ⚠️" : "fallback: " + r.fallback)}</span>`;
    }

    let actionHtml = "";
    if (!r.required && r.value) {
      actionHtml = `<button type="button" class="role-unassign-btn" data-role="${escapeAttr(r.id)}">Unassign</button>`;
    }

    card.innerHTML = `
      <div class="role-assignment-left">
        <span class="role-assignment-title">${escapeHtml(r.name)}</span>
        <span class="role-assignment-desc">${escapeHtml(r.desc)}</span>
      </div>
      <div class="role-assignment-right">
        ${badgeHtml}
        ${actionHtml}
      </div>
    `;
    container.appendChild(card);
  }

  container.querySelectorAll(".role-unassign-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      const role = btn.dataset.role;
      const stateKey = role === "vision" ? "visionModel" : role === "audio" ? "audioModel" : role === "learning" ? "learningModel" : role === "subagent" ? "subagentModel" : role === "image" ? "imageModel" : "embeddingsModel";
      const lsKey = role === "vision" ? "ollamabot.visionModel" : role === "audio" ? "ollamabot.audioModel" : role === "learning" ? "ollamabot.learningModel" : role === "subagent" ? "ollamabot.subagentModel" : role === "image" ? "ollamabot.imageModel" : "ollamabot.embeddingsModel";

      state[stateKey] = "";
      localStorage.setItem(lsKey, "");

      saveRoleModels();
      renderActive();
      renderModels();
      renderRoleAssignments();
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

function addFileAttachments(files) {
  for (const file of files) {
    // Skip images and audio — those go through their dedicated flows
    if (file.type.startsWith("image/") || file.type.startsWith("audio/")) {
      addSystemMessage(`Use the Image or Audio button for ${file.name}.`);
      renderMessages();
      continue;
    }
    // Store the File object locally; upload happens at send-time
    state.attachments.push({
      name: file.name,
      mime: file.type || "application/octet-stream",
      kind: "file",
      path: "",
      size: file.size,
      data: "",
      url: "",
      _file: file,   // raw File object, not serialised
    });
  }
  els.fileInput.value = "";
  renderAttachments();
}

function uploadFileWithProgress(att, sessionId) {
  return new Promise((resolve) => {
    const formData = new FormData();
    formData.append("file", att._file);

    const xhr = new XMLHttpRequest();
    xhr.open("POST", `/api/sessions/${encodeURIComponent(sessionId)}/upload`);

    xhr.upload.addEventListener("progress", (e) => {
      if (e.lengthComputable) {
        att._uploading = Math.round((e.loaded / e.total) * 100);
        renderAttachments();
      }
    });

    xhr.addEventListener("load", () => {
      delete att._uploading;
      delete att._file;
      if (xhr.status >= 200 && xhr.status < 300) {
        try {
          const info = JSON.parse(xhr.responseText);
          att.name = info.name;
          att.mime = info.mime;
          att.path = info.path;
          att.size = info.size;
        } catch {
          addSystemMessage(`❌ Upload parse error for ${att.name}`);
          renderMessages();
        }
      } else {
        addSystemMessage(`❌ Upload failed for ${att.name}: ${xhr.responseText}`);
        renderMessages();
      }
      renderAttachments();
      resolve();
    });

    xhr.addEventListener("error", () => {
      delete att._uploading;
      delete att._file;
      addSystemMessage(`❌ Upload error for ${att.name}: network error`);
      renderMessages();
      renderAttachments();
      resolve();
    });

    att._uploading = 0;
    renderAttachments();
    xhr.send(formData);
  });
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
  await waitForBootstrap();
  const content = els.prompt.value.trim();
  if (content.startsWith("/goal")) {
    els.prompt.value = "";
    els.prompt.focus();

    if (!state.activeSessionId) {
      const title = content.slice(0, 40);
      await createSession(title);
    }

    const command = content.slice(5).trim();
    let action = "status";
    let objective = "";

    if (command === "pause") {
      action = "pause";
    } else if (command === "resume") {
      action = "resume";
    } else if (command === "clear") {
      action = "clear";
    } else if (command === "") {
      action = "status";
    } else {
      action = "start";
      objective = command;
    }

    try {
      if (action === "status") {
        const res = await fetch(`/api/sessions/${encodeURIComponent(state.activeSessionId)}`);
        if (res.ok) {
          const sess = await res.json();
          if (sess.goal_objective) {
            addSystemMessage(`🎯 **Active Goal:** ${sess.goal_objective}\n\n**Status:** \`${sess.goal_status}\`\n\n**Last evaluation check:** ${sess.goal_reasoning || "None"}`);
          } else {
            addSystemMessage("ℹ️ No active goal in this session. Start one with `/goal <objective>`");
          }
          renderMessages();
        }
      } else {
        const res = await fetch(`/api/sessions/${encodeURIComponent(state.activeSessionId)}/goal`, {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ action, objective }),
        });
        if (res.ok) {
          const sess = await res.json();
          state.messages = sess.messages || [];
          renderMessages();
          updateContextBar();
        } else {
          const errText = await res.text();
          addSystemMessage(`❌ Error executing goal: ${errText}`);
          renderMessages();
        }
      }
    } catch (err) {
      addSystemMessage(`❌ Error: ${err.message}`);
      renderMessages();
    }
    return;
  }
  console.log("[sendMessage] Triggered. content_len:", content.length, "attachments:", state.attachments.length, "mainModel:", state.settings?.model_default || state.activeModel);
  if (state.attachments.length > 0) {
    console.log("[sendMessage] Attachment kinds:", state.attachments.map(a => a.kind), "data_lens:", state.attachments.map(a => a.data?.length || 0));
  }
  if (!content && state.attachments.length === 0) return;

  // Ensure we have an active session before sending
  if (!state.activeSessionId || !state.sessions.some((s) => s.id === state.activeSessionId)) {
    await ensureActiveSession();
  }

  const beforeFilterCount = state.attachments.length;
  // File-kind attachments are already saved server-side; keep them in the
  // visible record but do not pass them through capabilityFor.
  const fileAttachments = state.attachments.filter((a) => a.kind === "file");
  const mediaAttachments = state.attachments.filter((a) => a.kind !== "file" && capabilityFor(a.kind));
  console.log(`[sendMessage] After filter: ${mediaAttachments.length} media + ${fileAttachments.length} file attachments (from ${beforeFilterCount})`);
  const images = mediaAttachments.map((attachment) => attachment.data);
  const visibleAttachments = [...mediaAttachments, ...fileAttachments];
  console.log("[sendMessage] images array length:", images.length, "first image data length:", images[0]?.length || 0);
  
  // Push the message with processed = false to state
  const userMessage = { role: "user", content, images, attachments: visibleAttachments, processed: false, timestamp: new Date().toISOString() };
  state.messages.push(userMessage);
  
  state.attachments = [];
  els.prompt.value = "";
  els.prompt.focus();
  renderAttachments();
  renderMessages();
  updateContextBar();
  if (mediaAttachments.length) {
    addSystemMessage(`Attached ${mediaAttachments.map((item) => item.kind).join(", ")} using Ollama multimodal payload.`);
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

  await waitForBootstrap();

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
    if (msg.role === "user" || msg.role === "assistant" || msg.role === "tool") {
      // Never re-send audio binary data in history: its transcription replaces
      // it. Images are kept; the backend drops them when vision routing is
      // active (the description already lives in the history).
      const atts = msg.attachments || [];
      let images = msg.images || [];
      let kinds = atts.map((a) => a.kind);
      if (atts.length && images.length) {
        const keptImages = [];
        const keptKinds = [];
        images.forEach((img, i) => {
          const kind = kinds[i] || "image";
          if (kind === "audio") return;
          keptImages.push(img);
          keptKinds.push(kind);
        });
        images = keptImages;
        kinds = keptKinds;
      }

      let historyContent = msg.content || "";
      if (!historyContent && msg.role === "user") {
        const audioTexts = atts
          .filter((a) => a.kind === "audio" && a.transcription)
          .map((a) => a.transcription);
        if (audioTexts.length > 0) {
          historyContent = audioTexts.map((t) => `[Audio transcription: ${t}]`).join("\n");
        }
      }

      outboundMessages.push({
        role: msg.role,
        content: historyContent,
        images: images.length ? images : undefined,
        image_kinds: images.length ? kinds : undefined,
        tool_calls: msg.tool_calls || undefined,
        name: msg.name || undefined,
      });
    }
  }

  // Upload any pending file attachments BEFORE inserting the assistant bubble,
  // so the staging-area pills are still live and show upload progress.
  const pendingFiles = nextItem.attachments?.filter((a) => a.kind === "file" && a._file) || [];
  if (pendingFiles.length > 0) {
    if (!state.activeSessionId || state.activeSessionId.startsWith("client_")) {
      await ensureActiveSession();
    }
    for (const att of pendingFiles) {
      await uploadFileWithProgress(att, state.activeSessionId);
    }
    // Re-render messages so in-history pills switch from pending div to download link
    renderMessages();
  }

  if (!state.activeSessionId || state.activeSessionId.startsWith("client_")) {
    await ensureActiveSession();
  }

  await saveSession();

  let assistant = { role: "assistant", model: state.settings?.model_default || state.activeModel || "", channel: "web", content: "", steps: [], streaming: true, waiting: true, metrics: null };

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
  const firstImageData = currentMsg?.images?.[0];
  console.log("[processQueue] Sending to /api/chat/stream:", {
    model: state.settings?.model_default || state.activeModel,
    totalMessages: outboundMessages.length,
    currentMsg: {
      role: currentMsg?.role,
      content_len: currentMsg?.content?.length || 0,
      images_count: currentMsg?.images?.length || 0,
      first_image_data_len: firstImageData ? firstImageData.length : 0,
      first_image_data_preview: firstImageData ? firstImageData.substring(0, 50) + "..." : "none",
      image_kinds: currentMsg?.image_kinds,
    }
  });

  try {
    const response = await fetch("/api/chat/stream", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        messages: outboundMessages,
        session_id: state.activeSessionId || "",
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
      tool_approval_required: (value) => {
        showApprovalDialog(value.id, value.tool, value.arguments);
      },
      tool_clarification_required: (value) => {
        showClarificationDialog(value.id, value.question, value.options);
      },
      tool_plan_confirmation: (value) => {
        showPlanConfirmationCard(value.id, value.summary, value.steps);
      },
      plan_progress: (value) => {
        state.activePlan = value?.active_plan || null;
        renderMessages();
      },
      media_pre_processing: (value) => {
        // Structured payload: { summary, attachments: [{index, kind, action,
        // model, transcription, language, unreadable, note, description}] }.
        // Legacy string payloads (old sessions/servers) are shown as-is.
        const data = typeof value === "string" ? { summary: value, attachments: [] } : (value || {});
        const results = Array.isArray(data.attachments) ? data.attachments : [];

        // Map per-attachment results onto the active user message attachments.
        if (nextItem && results.length) {
          const atts = nextItem.attachments || [];
          const now = new Date().toISOString();
          for (const r of results) {
            const att = atts[r.index];
            if (!att || att.kind !== r.kind) continue;
            if (r.kind === "audio") {
              att.transcription = r.transcription || "";
              att.unreadable = !!r.unreadable;
            }
            if (r.kind === "image") {
              att.description = r.description || "";
            }
            att.processed_by = r.model || "";
            att.processed_at = now;
            att.routed = r.action === "transcribed" || r.action === "described";
          }
        }

        renderMessages();
      },
      assistant_turn: (value) => {
        // Finalize the current assistant bubble and start a new one for the next agent turn.
        assistant.streaming = false;
        assistant.waiting = false;
        if (!assistant.timestamp) {
          assistant.timestamp = new Date().toISOString();
        }
        const newAssistant = {
          role: "assistant",
          model: value?.model || state.activeModel || "",
          channel: "web",
          content: "",
          steps: [],
          streaming: true,
          waiting: true,
          metrics: null
        };
        state.messages.push(newAssistant);
        assistant = newAssistant;
        renderMessages();
      },
      thinking: (value) => {
        const lastStep = assistant.steps[assistant.steps.length - 1];
        if (lastStep && lastStep.type === "thinking") {
          lastStep.content += value;
        } else {
          assistant.steps.push({ type: "thinking", content: value });
        }
        renderMessages();
      },
      content: (value) => {
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
      image_progress: (value) => {
        // Show image generation progress with fixed-width format
        // Each generation has its own unique ID
        const genID = value.gen_id || "unknown";
        const completed = value.completed || 0;
        const total = value.total || 4;
        const percent = Math.round((completed / total) * 100);
        const text = `Generating image... ${percent}% (${completed}/${total})`;

        // Find step by generation ID to update existing or create new
        let step = assistant.steps.find(s => s.type === "image_progress" && s.genID === genID);
        if (!step) {
          step = {
            type: "image_progress",
            genID: genID,
            content: text,
            status: "running",
            completed: completed,
            total: total,
            percent: percent,
            width: value.width || 0,
            height: value.height || 0
          };
          assistant.steps.push(step);
        } else if (step.status === "running") {
          // Only update if still running - don't reactivate done/error bars
          step.content = text;
          step.completed = completed;
          step.total = total;
          step.percent = percent;
          if (value.width) step.width = value.width;
          if (value.height) step.height = value.height;
        }
        renderMessages();
      },
      image_complete: (value) => {
        // Image generation completed - find by genID
        const genID = value.gen_id || "unknown";
        // Convert relative path to absolute URL using current page origin
        const absURL = value.path ? new URL(value.path, window.location.origin).href : "";
        let step = assistant.steps.find(s => s.type === "image_progress" && s.genID === genID);
        if (step && step.status !== "error") {
          step.content = `Image generated!`;
          step.imageURL = absURL;
          step.status = "done";
          if (value.width) step.width = value.width;
          if (value.height) step.height = value.height;
        } else if (!step) {
          assistant.steps.push({
            type: "image_progress",
            genID: genID,
            content: `Image generated!`,
            imageURL: absURL,
            status: "done",
            width: value.width || 0,
            height: value.height || 0
          });
        }
        renderMessages();
      },
      image_error: (value) => {
        // Image generation failed - find by genID
        const genID = value.gen_id || "unknown";
        let step = assistant.steps.find(s => s.type === "image_progress" && s.genID === genID);
        if (step) {
          step.content = `Image generation failed\n${value.error}`;
          step.status = "error";
        } else {
          assistant.steps.push({ type: "image_progress", genID: genID, content: `❌ Image generation failed\n${value.error}`, status: "error" });
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
      context_optimization_start: (value) => {
        addSystemMessage(`🔄 **Optimizing context...**\nCurrently using ${value.tokens} tokens (${value.percent.toFixed(1)}% of model capacity). Synthesizing previous history to free up space...`);
        renderMessages();
        updateContextBar();
      },
      context_optimization_end: (value) => {
        addSystemMessage(`✅ **Context optimized!**\nNew context size: ${value.tokens} tokens (${value.percent.toFixed(1)}% of capacity).\nOptimization took: ${value.duration.toFixed(2)}s.`);
        renderMessages();
        updateContextBar();
      },
      error: (value) => {
        assistant.waiting = false;
        assistant.streaming = false;
        assistant.content += `\nError: ${value}`;
        renderMessages();
      },
      done: (value) => {
        if (value && Object.prototype.hasOwnProperty.call(value, "active_plan")) {
          state.activePlan = value.active_plan || null;
        }
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
        assistant.streaming = false;
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
    assistant.timestamp = new Date().toISOString();
    renderMessages();
    updateContextBar();
    // Transcriptions are always provided by the backend via the structured
    // media_pre_processing event (even in passthrough mode), so no fallback
    // hack is needed here. Audio base64 is filtered out of outbound history
    // per-attachment; attachments keep their data for playback/persistence.

    await loadSession(state.activeSessionId);
    await loadModels();

    state.isProcessing = false;
    state.currentAbortController = null;
    updateComposerUI();

    // Process next item in the queue!
    processNextQueueItem();
  }
}

// Returns true if any assistant response is currently in progress (local or remote)
function isAnyAssistantInProgress() {
  const lastMsg = state.messages[state.messages.length - 1];
  if (lastMsg && lastMsg.role === "assistant") {
    const hasMetrics = lastMsg.metrics && lastMsg.metrics.total_duration;
    if (!hasMetrics) {
      const ts = lastMsg.timestamp ? new Date(lastMsg.timestamp) : null;
      const now = new Date();
      if (ts && (now - ts) < 60000) {
        return true;
      }
    }
  }
  return false;
}

function updateComposerUI() {
  const processing = state.isProcessing || isAnyAssistantInProgress();
  const bootstrapping = !state.bootstrapReady;
  if (els.sendBtn) {
    els.sendBtn.disabled = bootstrapping;
    els.sendBtn.title = bootstrapping ? "Loading settings..." : "";
  }
  if (bootstrapping) {
    if (els.cacheState) {
      els.cacheState.textContent = "loading...";
      els.cacheState.style.borderColor = "var(--muted)";
      els.cacheState.style.color = "var(--muted)";
    }
    return;
  }
  if (processing) {
    if (els.skipBtn) els.skipBtn.style.display = "inline-flex";
    if (els.sendBtn) els.sendBtn.textContent = "Queue";
    if (els.cacheState) {
      els.cacheState.textContent = "processing...";
      els.cacheState.style.borderColor = "var(--accent-2)";
      els.cacheState.style.color = "var(--accent-2)";
    }
  } else {
    if (els.skipBtn) els.skipBtn.style.display = "none";
    if (els.sendBtn) els.sendBtn.textContent = "Send";
    if (els.cacheState) {
      if (els.baseUrl.textContent === "Ollama: Offline") {
        els.cacheState.textContent = "offline";
        els.cacheState.style.borderColor = "var(--bad)";
        els.cacheState.style.color = "var(--bad)";
      } else {
        els.cacheState.textContent = "ready";
        els.cacheState.style.borderColor = "var(--accent)";
        els.cacheState.style.color = "var(--accent)";
      }
    }
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
    
    const imageAnalysisMatch = part.match(/^\[Image \d+ analysis by ([^\]]+)\]:/);
    if (imageAnalysisMatch) {
      const body = part.slice(part.indexOf("]:") + 2).trim();
      html += `
        <div class="analysis-box image-analysis">
          <div class="analysis-box-head">
            <span class="analysis-icon">🖼️</span>
            <strong>Image Context Analysis</strong>
            <span class="analysis-tag">vision model: ${escapeHtml(imageAnalysisMatch[1])}</span>
          </div>
          <div class="analysis-box-body">${renderMarkdown(body)}</div>
        </div>
      `;
    } else if (part.startsWith("[Audio Transcription & Analysis]:")) {
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
  const grouped = groupMessagesAndTools(state.messages);

  // Detect if the last message is an assistant still in progress (e.g. from Telegram)
  const lastMsg = state.messages[state.messages.length - 1];
  let lastAssistantInProgress = false;
  if (lastMsg && lastMsg.role === "assistant") {
    const hasMetrics = lastMsg.metrics && lastMsg.metrics.total_duration;
    if (!hasMetrics) {
      const ts = lastMsg.timestamp ? new Date(lastMsg.timestamp) : null;
      const now = new Date();
      if (ts && (now - ts) < 60000) {
        lastAssistantInProgress = true;
      }
    }
  }

  let msgIdx = 0;
  for (const message of grouped) {
    if (message.role === "system") {
      if (isInternalSystemMessage(message.content)) continue;
      const div = document.createElement("article");
      div.className = `message system`;
      div.innerHTML = `<div class="message-content">${renderMarkdown(message.content)}</div>`;
      els.messages.appendChild(div);
      continue;
    }
    const div = document.createElement("article");
    const isQueued = message.role === "user" && message.processed === false;
    const isPreProcessing = message.role === "assistant" && message.content && (
      message.content.startsWith("The user has attached media. The pre-processing analysis is as follows:") ||
      message.content.startsWith("Media pre-processing context")
    );

    // For remote (Telegram) in-progress messages, synthesize waiting/streaming states
    const isLastMsg = message === grouped[grouped.length - 1];
    const isRemoteProcessing = lastAssistantInProgress && isLastMsg;
    const effectiveWaiting = message.waiting || isRemoteProcessing;
    const effectiveStreaming = message.streaming || isRemoteProcessing;

    div.className = `message ${message.role} ${effectiveStreaming ? "streaming" : ""} ${isQueued ? "queued" : ""} ${isPreProcessing ? "preprocessing" : ""}`;
    const pending = effectiveWaiting ? `<div class="waiting"><span></span><span></span><span></span><em>processing</em></div>` : "";
    const media = message.attachments?.length ? `<div class="message-media">${message.attachments.map(attachmentPreview).join("")}</div>` : "";
    const cursor = effectiveStreaming ? `<span class="stream-cursor"></span>` : "";

    // Build steps HTML (interleaved thinking / tool blocks).
    const steps = message.steps || [];
    const hideInlinePlanSteps = message.role === "assistant" && isLastMsg && state.activePlan && state.activePlan.status === "active";
    const stepsHtml = steps
      .filter((s) => !(hideInlinePlanSteps && s.type === "plan"))
      .map((s, idx) => {
      const isLastStep = idx === steps.length - 1;
      return renderStep(s, effectiveStreaming, isLastStep);
    }).join("");
    const activePlanHtml = message.role === "assistant" && isLastMsg && state.activePlan && state.activePlan.status === "active"
      ? renderPlanChecklist(state.activePlan, "progress")
      : "";
    // Legacy fallback: if no steps but has old-style thinking/toolCalls/toolResults, render them.
    let legacyHtml = "";
    if (!message.steps?.length) {
      if (message.thinking) {
        legacyHtml += `<details class="step step-thinking"><summary>💭 thinking</summary><pre>${escapeHtml(message.thinking)}</pre></details>`;
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
    const timeHtml = message.timestamp ? `<span class="message-time">${escapeHtml(formatMessageTime(message.timestamp))}</span>` : "";
    const metaHtml = `
      <div class="message-meta-container">
        <button class="message-copy-btn" type="button" title="Copy raw markdown">📋</button>
        ${timeHtml}
      </div>
    `;
    div.innerHTML = `<span class="role">${escapeHtml(roleName)}${queuedBadge}</span>${media}${pending}${activePlanHtml}${stepsHtml || legacyHtml}${contentHtml}${metricsHtml}${metaHtml}`;
    els.messages.appendChild(div);
    msgIdx++;
  }
  els.messages.scrollTop = els.messages.scrollHeight;
}

function renderStep(step, isLive = false, isLastStep = false) {
  switch (step.type) {
    case "thinking": {
      const isOpen = isLive && isLastStep;
      return `<details class="step step-thinking" ${isOpen ? "open" : ""}><summary>💭 thinking</summary><pre>${escapeHtml(step.content || "")}</pre></details>`;
    }
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
      const showRunning = isLive && step.status === "running";
      const statusLabel = showRunning ? "running..." : "";
      const statusClass = showRunning ? "running" : "";
      let argsText = "";
      let parsedArgs = null;
      if (step.arguments) {
        try {
          parsedArgs = typeof step.arguments === "string" ? JSON.parse(step.arguments) : step.arguments;
          argsText = JSON.stringify(parsedArgs, null, 2);
        } catch {
          argsText = String(step.arguments || "");
        }
      }
      if (step.name === "present_plan" && parsedArgs) {
        return renderPlanChecklist({
          summary: parsedArgs.summary || "",
          steps: parsedArgs.steps || [],
          completed: 0,
          status: "active",
        }, "inline");
      }
      const resultText = step.result !== null && step.result !== undefined ? escapeHtml(String(step.result)) : "";
      const argsHtml = argsText ? `<pre class="step-tool-args">${escapeHtml(argsText)}</pre>` : "";
      const resultHtml = resultText ? `
        <details class="step-tool-result-details">
          <summary>📄 Show tool response (${formatBytes(resultText.length)})</summary>
          <pre class="step-tool-result-text">${resultText}</pre>
        </details>
      ` : (showRunning ? `<div class="step-tool-running"><span></span><span></span><span></span></div>` : "");
      const statusBadge = statusLabel ? ` <span class="step-tool-status ${statusClass}">${statusLabel}</span>` : "";
      return `<details class="step step-tool-exec ${statusClass}"><summary><span class="step-tool-icon">⚙️</span> ${escapeHtml(step.name || "unknown")}${statusBadge}</summary>${argsHtml}${resultHtml}</details>`;
    }
    case "plan": {
      return renderPlanChecklist({
        summary: step.content || "",
        steps: step.plan_steps || [],
        completed: step.completed || 0,
        status: step.status || "active",
      }, "inline");
    }
    case "image_progress": {
      let status = step.status;
      if (!status) {
        status = step.imageURL ? "done" : "error";
      }

      const w = step.width || 512;
      const h = step.height || 512;

      if (status === "done") {
        return `
          <div class="step step-image-progress done">
            <div class="image-gen-completed" style="aspect-ratio: ${w} / ${h};">
              <img src="${escapeHtml(step.imageURL)}" alt="Generated image" class="generated-image" data-url="${escapeHtml(step.imageURL)}" />
            </div>
          </div>
        `;
      } else if (status === "error") {
        return `
          <div class="step step-image-progress error">
            <div class="image-gen-placeholder error-placeholder" style="aspect-ratio: ${w} / ${h};">
              <div class="placeholder-overlay error-overlay">
                <span class="error-icon">⚠️</span>
                <span class="status-text">${escapeHtml(step.content || "Image generation failed")}</span>
              </div>
            </div>
          </div>
        `;
      } else {
        const pct = step.percent || 0;
        return `
          <div class="step step-image-progress running">
            <div class="image-gen-placeholder" style="aspect-ratio: ${w} / ${h};">
              <div class="gradient-bg">
                <div class="blob blob-1"></div>
                <div class="blob blob-2"></div>
                <div class="blob blob-3"></div>
              </div>
              <div class="noise-overlay"></div>
              <div class="placeholder-overlay">
                <div class="status-info">
                  <span class="status-spinner"></span>
                  <span class="status-text">${escapeHtml(step.content || "Generating image...")}</span>
                </div>
                <div class="progress-bar-container">
                  <div class="progress-bar-fill" style="width: ${pct}%;"></div>
                </div>
              </div>
            </div>
          </div>
        `;
      }
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
  const resultHtml = result ? `
    <details class="step-tool-result-details">
      <summary>📄 Show tool response (${formatBytes(result.length)})</summary>
      <pre class="step-tool-result-text">${result}</pre>
    </details>
  ` : "";
  return `<details class="step step-tool-exec ${statusClass}"><summary><span class="step-tool-icon">⚙️</span> ${escapeHtml(tr.name || "unknown")} <span class="step-tool-status ${statusClass}">${status}</span></summary>${resultHtml}</details>`;
}

function isInternalSystemMessage(content) {
  if (!content) return false;
  return (
    content.includes("The current session contains the following attachments") ||
    content.includes("The user has uploaded the following files to this session")
  );
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
  const escaped = escapeHtml(sanitizeMath(text));
  const lines = escaped.split("\n");
  let inCode = false;
  const html = [];
  for (const line of lines) {
    if (line.startsWith("```")) {
      if (inCode) {
        html.push("</code></pre></div>");
      } else {
        const lang = line.slice(3).trim() || "code";
        html.push(`<div class="code-block-wrapper"><div class="code-block-header"><span class="code-block-lang">${lang}</span><button class="code-block-copy-btn" type="button" title="Copy code">Copy</button></div><pre><code>`);
      }
      inCode = !inCode;
      continue;
    }
    if (inCode) {
      html.push(`${line}\n`);
      continue;
    }
    if (isHorizontalRule(line)) {
      html.push("<hr>");
      continue;
    }
    if (line.startsWith("### ")) html.push(`<h3>${inlineMd(line.slice(4))}</h3>`);
    else if (line.startsWith("## ")) html.push(`<h2>${inlineMd(line.slice(3))}</h2>`);
    else if (line.startsWith("# ")) html.push(`<h1>${inlineMd(line.slice(2))}</h1>`);
    else if (line.startsWith("- ")) html.push(`<p class="li">• ${inlineMd(line.slice(2))}</p>`);
    else if (line.trim() === "") html.push("<br>");
    else html.push(`<p>${inlineMd(line)}</p>`);
  }
  if (inCode) html.push("</code></pre></div>");
  return html.join("");
}

// simplifyLatex strips LaTeX command wrappers and replaces operators with Unicode.
function simplifyLatex(s) {
  return s
    .replace(/\\frac\{([^}]+)\}\{([^}]+)\}/g, "$1/$2")
    .replace(/\\sqrt\{([^}]+)\}/g, "√$1")
    .replace(/\\text\{([^}]+)\}/g, "$1")
    .replace(/\\div/g, "÷")
    .replace(/\\times/g, "×")
    .replace(/\\cdot/g, "·")
    .replace(/\\pm/g, "±")
    .replace(/\\leq/g, "≤")
    .replace(/\\geq/g, "≥")
    .replace(/\\neq/g, "≠")
    .replace(/\\approx/g, "≈")
    .replace(/\\infty/g, "∞")
    .replace(/\\pi/g, "π")
    .replace(/\\alpha/g, "α")
    .replace(/\\beta/g, "β")
    .replace(/\\gamma/g, "γ")
    .replace(/\\delta/g, "δ")
    .replace(/\\sigma/g, "σ")
    .replace(/\\theta/g, "θ")
    .replace(/\\left/g, "")
    .replace(/\\right/g, "")
    .replace(/\\[a-zA-Z]+/g, "")
    .replace(/[{}]/g, "")
    .trim();
}

// sanitizeMath converts LaTeX math delimiters ($...$, $$...$$, \[...\], \(...\))
// to plain readable text so raw notation doesn't appear in the UI.
function sanitizeMath(text) {
  // Block: $$...$$ — must come before single $
  text = text.replace(/\$\$([\s\S]+?)\$\$/g, (_, inner) => simplifyLatex(inner.trim()));
  // Block: \[...\]
  text = text.replace(/\\\[([\s\S]+?)\\\]/g, (_, inner) => simplifyLatex(inner.trim()));
  // Inline: $...$ (single, no newline inside)
  text = text.replace(/\$([^$\n]+?)\$/g, (_, inner) => simplifyLatex(inner));
  // Inline: \(...\)
  text = text.replace(/\\\((.+?)\\\)/g, (_, inner) => simplifyLatex(inner));
  return text;
}

function isHorizontalRule(line) {
  return /^\s*(\*{3,}|-{3,}|_{3,})\s*$/.test(line);
}

function inlineMd(text) {
  return text
    .replace(/\*\*(.+?)\*\*/g, "<strong>$1</strong>")
    .replace(/`(.+?)`/g, "<code>$1</code>")
    .replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener noreferrer">$1</a>');
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
    let transcriptionHtml = "";
    if (attachment.transcription) {
      transcriptionHtml = `<div class="audio-transcription">${escapeHtml(attachment.transcription)}</div>`;
    } else if (attachment.unreadable) {
      transcriptionHtml = `<div class="audio-transcription unreadable">Audio could not be transcribed.</div>`;
    }
    return `<div class="media-preview audio" ${stopAll}><span>${label}</span><audio controls preload="metadata" src="${escapeAttr(attachment.url)}" ${stopAll}></audio>${transcriptionHtml}</div>`;
  }
  if (attachment.kind === "file") {
    const uploading = attachment._uploading;
    const mimeIcon = fileMimeIcon(attachment.mime || "");
    const sizeStr = attachment.size ? formatFileSize(attachment.size) : "";
    const statusStr = uploading != null ? `${uploading}%` : sizeStr;
    const hasPending = !!attachment._file;
    const inner = `<span class="file-icon">${mimeIcon}</span><span class="file-name">${label}</span><span class="file-size">${statusStr}</span>`;
    if (!hasPending && attachment.path) {
      const href = `/api/sessions/${encodeURIComponent(state.activeSessionId || "")}/uploads/${encodeURIComponent(attachment.name)}`;
      return `<a class="media-preview file" href="${escapeAttr(href)}" download="${escapeAttr(attachment.name)}" title="Download ${label}">${inner}</a>`;
    }
    return `<div class="media-preview file${hasPending ? " pending" : ""}">${inner}</div>`;
  }
  return `<div class="media-preview"><span>${label}</span></div>`;
}

function formatFileSize(bytes) {
  if (bytes < 1024) return bytes + " B";
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + " KB";
  return (bytes / (1024 * 1024)).toFixed(1) + " MB";
}

function fileMimeIcon(mime) {
  if (mime.includes("pdf")) return "📄";
  if (mime.includes("video")) return "🎥";
  if (mime.includes("zip") || mime.includes("archive") || mime.includes("compressed")) return "🗃️";
  if (mime.includes("json") || mime.includes("xml")) return "{ }";
  if (mime.startsWith("text/")) return "📝";
  return "📁";
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
    const audioMics = devices.filter((d) => d.kind === "audioinput" && d.deviceId);
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

let approvalInterval = null;
let clarificationInterval = null;

function showApprovalDialog(id, toolName, args) {
  if (approvalInterval) {
    clearInterval(approvalInterval);
  }
  state.currentApprovalId = id;
  els.approvalToolName.textContent = toolName;
  try {
    els.approvalToolArgs.textContent = JSON.stringify(args, null, 2);
  } catch {
    els.approvalToolArgs.textContent = String(args);
  }
  
  const countdownEl = document.querySelector("#approvalCountdown");
  if (countdownEl) {
    const startTime = Date.now();
    const duration = 300 * 1000; // 5 minutes
    
    const updateTimer = () => {
      const elapsed = Date.now() - startTime;
      const remaining = Math.max(0, duration - elapsed);
      const totalSecs = Math.ceil(remaining / 1000);
      const mins = Math.floor(totalSecs / 60);
      const secs = totalSecs % 60;
      countdownEl.textContent = `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
      if (remaining <= 0) {
        clearInterval(approvalInterval);
        approvalInterval = null;
        respondToApproval(id, false); // Auto-deny
      }
    };
    updateTimer();
    approvalInterval = setInterval(updateTimer, 500);
  }
  
  els.approvalDialog.showModal();
}

async function respondToApproval(id, approved) {
  if (approvalInterval) {
    clearInterval(approvalInterval);
    approvalInterval = null;
  }
  try {
    await fetch("/api/tools/approve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, approved }),
    });
  } catch (err) {
    console.error("Failed to send tool approval response:", err);
  } finally {
    state.currentApprovalId = null;
    els.approvalDialog.close();
  }
}

function showClarificationDialog(id, question, options) {
  if (clarificationInterval) {
    clearInterval(clarificationInterval);
  }
  state.currentClarificationId = id;
  els.clarificationQuestion.textContent = question;
  els.clarificationOptionsContainer.innerHTML = "";
  
  // Reset custom input
  els.clarificationCustomInput.value = "";
  
  // Create option buttons
  options.forEach(opt => {
    const btn = document.createElement("button");
    btn.textContent = opt;
    btn.addEventListener("click", () => respondToClarification(id, opt));
    els.clarificationOptionsContainer.appendChild(btn);
  });
  
  // Setup cancel button
  els.clarificationCancel.onclick = () => {
    clearClarification();
    els.clarificationCard.style.display = "none";
    els.messages.appendChild(els.clarificationCard);
  };
  
  // Setup custom send button
  els.clarificationSendCustom.onclick = () => {
    const customText = els.clarificationCustomInput.value.trim();
    if (customText) {
      respondToClarification(id, customText);
    }
  };
  
  // Handle Enter key in custom input
  els.clarificationCustomInput.onkeydown = (e) => {
    if (e.key === "Enter") {
      els.clarificationSendCustom.click();
    }
  };
  
  // Insert card into messages (before composer so it appears inline)
  const messagesContainer = els.messages;
  const existingCard = els.clarificationCard;
  if (existingCard.parentNode !== messagesContainer) {
    messagesContainer.appendChild(existingCard);
  }
  existingCard.style.display = "block";
  
  // Scroll to show the card
  existingCard.scrollIntoView({ behavior: "smooth", block: "center" });
  
  // Auto-answer timer
  const countdownEl = document.querySelector("#clarificationCountdown");
  if (countdownEl) {
    const startTime = Date.now();
    const duration = 300 * 1000; // 5 minutes
    
    const updateTimer = () => {
      const elapsed = Date.now() - startTime;
      const remaining = Math.max(0, duration - elapsed);
      const totalSecs = Math.ceil(remaining / 1000);
      const mins = Math.floor(totalSecs / 60);
      const secs = totalSecs % 60;
      countdownEl.textContent = `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
      if (remaining <= 0) {
        clearInterval(clarificationInterval);
        clarificationInterval = null;
        const chosen = selectDefaultOptionJS(options);
        respondToClarification(id, chosen);
      }
    };
    updateTimer();
    clarificationInterval = setInterval(updateTimer, 500);
  }
}

function clearClarification() {
  if (clarificationInterval) {
    clearInterval(clarificationInterval);
    clarificationInterval = null;
  }
  state.currentClarificationId = null;
}

function selectDefaultOptionJS(options) {
  if (!options || options.length === 0) return "";
  for (const opt of options) {
    const low = opt.toLowerCase();
    if (low.includes("recommended") || low.includes("recomendado") || low.includes("default") || low.includes("predeterminado")) {
      return opt;
    }
  }
  return options[0];
}

async function respondToClarification(id, option) {
  clearClarification();
  try {
    await fetch("/api/tools/clarify", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, option }),
    });
  } catch (err) {
    console.error("Failed to send clarification response:", err);
  } finally {
    // Hide the inline card
    els.clarificationCard.style.display = "none";
    els.messages.appendChild(els.clarificationCard);
  }
}

let planConfirmationInterval = null;

function renderPlanChecklist(plan, variant = "inline") {
  const summary = plan?.summary || "";
  const steps = Array.isArray(plan?.steps) ? plan.steps : [];
  const completed = Math.max(0, Math.min(Number(plan?.completed || 0), steps.length));
  const status = plan?.status || "active";
  const items = steps.map((step, idx) => {
    let itemStatus = "pending";
    if (idx < completed) itemStatus = "done";
    else if (idx === completed && status === "active") itemStatus = "current";
    const marker = itemStatus === "done" ? "✓" : itemStatus === "current" ? "●" : "";
    return `
      <li class="plan-checklist-item ${itemStatus}">
        <span class="plan-checklist-marker">${marker}</span>
        <span class="plan-checklist-text">${escapeHtml(step)}</span>
      </li>
    `;
  }).join("");
  const summaryHtml = summary ? `<p class="plan-checklist-summary">${escapeHtml(summary)}</p>` : "";
  const statusHtml = status === "completed"
    ? `<span class="plan-checklist-status done">Completed</span>`
    : status === "rejected"
      ? `<span class="plan-checklist-status rejected">Rejected</span>`
      : `<span class="plan-checklist-status">Step ${Math.min(completed + 1, steps.length || 1)} of ${steps.length || 1}</span>`;
  return `
    <section class="plan-checklist ${variant}">
      <div class="plan-checklist-head">
        <span class="plan-checklist-title">Execution Plan</span>
        ${statusHtml}
      </div>
      ${summaryHtml}
      <ol class="plan-checklist-list">${items}</ol>
    </section>
  `;
}

function showPlanConfirmationCard(id, summary, steps) {
  if (planConfirmationInterval) {
    clearInterval(planConfirmationInterval);
  }
  state.currentPlanConfirmationId = id;
  els.planSummaryText.textContent = summary;
  els.planStepsContainer.innerHTML = renderPlanChecklist({ summary, steps, completed: 0, status: "active" }, "approval");
  
  els.planRejectBtn.onclick = () => {
    respondToPlanConfirmation(id, false);
  };

  els.planApproveBtn.onclick = () => {
    respondToPlanConfirmation(id, true);
  };
  
  const messagesContainer = els.messages;
  const existingCard = els.planConfirmationCard;
  if (existingCard.parentNode !== messagesContainer) {
    messagesContainer.appendChild(existingCard);
  }
  existingCard.style.display = "block";
  
  existingCard.scrollIntoView({ behavior: "smooth", block: "center" });
  
  const countdownEl = els.planCountdown;
  if (countdownEl) {
    const startTime = Date.now();
    const duration = 300 * 1000; // 5 minutes
    
    const updateTimer = () => {
      const elapsed = Date.now() - startTime;
      const remaining = Math.max(0, duration - elapsed);
      const totalSecs = Math.ceil(remaining / 1000);
      const mins = Math.floor(totalSecs / 60);
      const secs = totalSecs % 60;
      countdownEl.textContent = `${String(mins).padStart(2, '0')}:${String(secs).padStart(2, '0')}`;
      if (remaining <= 0) {
        clearInterval(planConfirmationInterval);
        planConfirmationInterval = null;
        respondToPlanConfirmation(id, true);
      }
    };
    updateTimer();
    planConfirmationInterval = setInterval(updateTimer, 500);
  }
}

function clearPlanConfirmation() {
  if (planConfirmationInterval) {
    clearInterval(planConfirmationInterval);
    planConfirmationInterval = null;
  }
  state.currentPlanConfirmationId = null;
}

async function respondToPlanConfirmation(id, approved) {
  clearPlanConfirmation();
  try {
    const res = await fetch("/api/tools/plan-confirm", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ id, approved }),
    });
    if (res.ok) {
      const data = await res.json();
      if (Object.prototype.hasOwnProperty.call(data, "active_plan")) {
        state.activePlan = data.active_plan || null;
        renderMessages();
      }
    }
  } catch (err) {
    console.error("Failed to send plan confirmation response:", err);
  } finally {
    els.planConfirmationCard.style.display = "none";
    els.messages.appendChild(els.planConfirmationCard);
  }
}

function normalizeRawMessages(rawMessages) {
  return (rawMessages || []).filter((m) => {
    const msg = typeof m === "string" ? JSON.parse(m) : m;
    return !(msg.role === "system" && isInternalSystemMessage(msg.content || ""));
  }).map((m) => {
    const msg = typeof m === "string" ? JSON.parse(m) : m;
    let steps = (msg.steps || []).map((s) => {
      const { status, ...rest } = s;
      return rest;
    });

    // Filter out duplicate tool_call steps if a corresponding tool_exec step exists
    steps = steps.filter((step) => {
      if (step.type === "tool_call") {
        const name = step.call?.function?.name;
        if (name && steps.some((other) => other.type === "tool_exec" && other.name === name)) {
          return false;
        }
      }
      return true;
    });

    // Prepend thinking step if present, even when steps already exist from backend
    if (msg.thinking && !steps.some((s) => s.type === "thinking")) {
      steps = [{ type: "thinking", content: msg.thinking }, ...steps];
    }
    // Convert legacy tool_calls / tool_results only if steps is empty
    if (!msg.steps || msg.steps.length === 0) {
      const tc = msg.toolCalls || msg.tool_calls || [];
      const tr = msg.toolResults || msg.tool_results || [];
      for (const call of tc) {
        steps.push({ type: "tool_call", call });
      }
      for (const res of tr) {
        steps.push({ type: "tool_exec", name: res.name, arguments: res.arguments, result: res.result });
      }
    }
    return {
      role: msg.role || "user",
      name: msg.name || undefined,
      content: msg.content || "",
      model: msg.model || "",
      channel: msg.channel || "",
      type: msg.type || "",
      steps,
      images: msg.images || undefined,
      tool_calls: msg.tool_calls || msg.toolCalls || undefined,
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
          url: url || "",
          transcription: att.transcription || "",
          description: att.description || "",
          processed_by: att.processed_by || "",
          processed_at: att.processed_at || "",
          unreadable: !!att.unreadable,
          size: att.size || 0,
          path: att.path || "",
        };
      }),
      streaming: false,
      waiting: false,
      timestamp: msg.timestamp || "",
      metrics: msg.metrics && msg.metrics.total_duration ? msg.metrics : null
    };
  });
}

function groupMessagesAndTools(messages) {
  const processed = [];
  for (const msg of messages) {
    if (msg.role === "tool" || msg.role === "tool_result") {
      let lastAssistant = null;
      for (let j = processed.length - 1; j >= 0; j--) {
        if (processed[j].role === "assistant") {
          lastAssistant = processed[j];
          break;
        }
      }

      if (lastAssistant) {
        let step = lastAssistant.steps.find(s => s.type === "tool_exec" && s.name === msg.name);
        if (!step) {
          let tcIdx = lastAssistant.steps.findIndex(s => s.type === "tool_call" && s.call?.function?.name === msg.name);
          if (tcIdx !== -1) {
            const tc = lastAssistant.steps[tcIdx];
            step = {
              type: "tool_exec",
              name: msg.name,
              arguments: tc.call?.function?.arguments,
              result: msg.content,
              status: "done"
            };
            lastAssistant.steps[tcIdx] = step;
          } else {
            step = {
              type: "tool_exec",
              name: msg.name,
              arguments: null,
              result: msg.content,
              status: "done"
            };
            lastAssistant.steps.push(step);
          }
        } else {
          step.result = msg.content;
          step.status = "done";
        }
      }
      continue;
    }

    if (msg.role === "assistant" && processed.length > 0 && processed[processed.length - 1].role === "assistant") {
      const lastAssistant = processed[processed.length - 1];

      // Concatenate content, separating with a newline if both have content
      if (lastAssistant.content && msg.content) {
        lastAssistant.content += "\n" + msg.content;
      } else if (msg.content) {
        lastAssistant.content = msg.content;
      }

      // Merge steps
      if (msg.steps && msg.steps.length > 0) {
        lastAssistant.steps = [...lastAssistant.steps, ...msg.steps.map(s => ({ ...s }))];
      }

      // Merge streaming/waiting states
      lastAssistant.streaming = lastAssistant.streaming || msg.streaming;
      lastAssistant.waiting = lastAssistant.waiting || msg.waiting;

      // Merge metrics
      if (msg.metrics) {
        if (!lastAssistant.metrics) {
          lastAssistant.metrics = {
            total_duration: 0,
            load_duration: 0,
            prompt_eval_count: 0,
            prompt_eval_duration: 0,
            eval_count: 0,
            eval_duration: 0,
          };
        }
        lastAssistant.metrics.total_duration += (msg.metrics.total_duration || 0);
        lastAssistant.metrics.load_duration += (msg.metrics.load_duration || 0);
        lastAssistant.metrics.prompt_eval_count += (msg.metrics.prompt_eval_count || 0);
        lastAssistant.metrics.prompt_eval_duration += (msg.metrics.prompt_eval_duration || 0);
        lastAssistant.metrics.eval_count += (msg.metrics.eval_count || 0);
        lastAssistant.metrics.eval_duration += (msg.metrics.eval_duration || 0);
      }

      continue;
    }

    const copy = {
      ...msg,
      steps: (msg.steps || []).map(s => ({ ...s }))
    };
    if (msg.metrics) {
      copy.metrics = { ...msg.metrics };
    }
    processed.push(copy);
  }
  return processed;
}

// ----- Sessions -----

async function loadSessions() {
  try {
    const response = await fetch("/api/sessions");
    if (!response.ok) return;
    const serverSessions = (await response.json()) || [];

    // If the active session isn't on the server yet (e.g. new session still
    // being processed), preserve it locally so it doesn't vanish from the
    // sidebar. Build a stub from whatever the server or local state has.
    if (state.activeSessionId && !serverSessions.some((s) => s.id === state.activeSessionId)) {
      const localCopy = (state.sessions || []).find((s) => s.id === state.activeSessionId);
      const stub = localCopy || { id: state.activeSessionId, title: "New session", updated_at: new Date().toISOString() };
      state.sessions = [stub, ...serverSessions.filter((s) => s.id !== state.activeSessionId)];
    } else {
      state.sessions = serverSessions;
    }

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
      body: JSON.stringify({ title }),
    });
    if (!response.ok) {
      console.warn("createSession: server returned non-OK response", response.status);
      return false;
    }
    const sess = await response.json();
    state.activeSessionId = sess.id;
    localStorage.setItem("ollamabot.activeSessionId", sess.id);
    state.messages = [];
    state.attachments = [];
    // Add the new session to the local list immediately to avoid race condition
    const sessionsList = state.sessions || [];
    const existingIndex = sessionsList.findIndex((s) => s.id === sess.id);
    if (existingIndex === -1) {
      sessionsList.unshift(sess);
      state.sessions = sessionsList;
    }
    renderSessions();
    renderMessages();
    renderAttachments();
    updateContextBar();
    await loadSessions();
    els.prompt.focus();
    return true;
  } catch (e) {
    console.warn("createSession failed:", e);
    return false;
  }
}

// Creates a temporary client-side only session as fallback
// This ensures the UI always has a session to work with
async function createClientSession() {
  const tempId = "client_" + Date.now();
  const sess = {
    id: tempId,
    title: "New session",
    model: state.settings?.model_default || state.activeModel,
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
  };
  state.activeSessionId = tempId;
  localStorage.setItem("ollamabot.activeSessionId", tempId);
  state.messages = [];
  state.attachments = [];
  const sessionsList = state.sessions || [];
  sessionsList.unshift(sess);
  state.sessions = sessionsList;
  renderSessions();
  renderMessages();
  renderAttachments();
  updateContextBar();
  console.log("[createClientSession] Created client-side session:", tempId);
}

async function loadSession(id) {
  try {
    const response = await fetch(`/api/sessions/${encodeURIComponent(id)}`);
    if (!response.ok) {
      // Session not found - clear state and create a new one
      if (response.status === 404) {
        state.activeSessionId = null;
        localStorage.removeItem("ollamabot.activeSessionId");
        state.messages = [];
        state.attachments = [];
        state.activePlan = null;
        await createSession();
      }
      return;
    }
    const sess = await response.json();
    state.activeSessionId = sess.id;
    localStorage.setItem("ollamabot.activeSessionId", sess.id);
    state.activePlan = sess.active_plan || null;
    state.messages = normalizeRawMessages(sess.messages || []);
    syncActiveModel();
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
      name: msg.name || undefined,
      content: msg.content || "",
      model: msg.model || undefined,
      channel: msg.channel || undefined,
      type: msg.type || undefined,
      tool_calls: msg.tool_calls || undefined,
      steps: (msg.steps || []).filter((step) => {
        if (step.type === "tool_call") {
          const name = step.call?.function?.name;
          if (name && (msg.steps || []).some((other) => other.type === "tool_exec" && other.name === name)) {
            return false;
          }
        }
        return true;
      }).map((s) => {
        const { status, ...rest } = s;
        return rest;
      }),
      images: msg.images || undefined,
      attachments: (msg.attachments || []).length ? msg.attachments.map((att) => ({
        name: att.name || "",
        mime: att.mime || "",
        kind: att.kind || "",
        data: att.data || "",
        url: att.url || (att.data ? `data:${att.mime || (att.kind === "audio" ? "audio/wav" : "image/png")};base64,${att.data}` : ""),
        transcription: att.transcription || "",
        description: att.description || "",
        processed_by: att.processed_by || "",
        processed_at: att.processed_at || "",
        unreadable: !!att.unreadable,
        path: att.path || "",
        size: att.size || 0,
      })) : undefined,
      image_kinds: msg.attachments?.map((a) => a.kind) || undefined,
      timestamp: msg.timestamp,
      metrics: msg.metrics || undefined,
    }));
    await fetch(`/api/sessions/${encodeURIComponent(state.activeSessionId)}`, {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ messages }),
    });
    // No loadSessions() here — the PUT triggers NotifyUpdate on the server,
    // which fires an SSE session_updated event that reloads the list.
  } catch (e) {
    console.warn("saveSession failed:", e);
  }
}

function formatRelativeTime(dateString) {
  if (!dateString) return "";
  const date = new Date(dateString);
  const now = new Date();
  const diffMs = now - date;
  const diffSecs = Math.floor(diffMs / 1000);
  const diffMins = Math.floor(diffSecs / 60);
  const diffHours = Math.floor(diffMins / 60);
  const diffDays = Math.floor(diffHours / 24);

  if (diffMs < 0) {
    return "just now";
  }
  if (diffSecs < 60) {
    return "just now";
  } else if (diffMins < 60) {
    return `${diffMins}m ago`;
  } else if (diffHours < 24) {
    return `${diffHours}h ago`;
  } else if (diffDays === 1) {
    return "yesterday";
  } else if (diffDays < 30) {
    return `${diffDays}d ago`;
  } else {
    return date.toLocaleDateString();
  }
}

function formatMessageTime(dateString) {
  if (!dateString) return "";
  const date = new Date(dateString);
  const now = new Date();
  const hours = date.getHours();
  const minutes = date.getMinutes();
  const ampm = hours >= 12 ? 'PM' : 'AM';
  const displayHours = hours % 12 || 12;
  const displayMinutes = minutes < 10 ? '0' + minutes : minutes;
  const timeStr = `${displayHours}:${displayMinutes} ${ampm}`;
  
  if (date.toDateString() === now.toDateString()) {
    return timeStr;
  }
  return `${date.toLocaleDateString(undefined, {month: 'short', day: 'numeric'})} ${timeStr}`;
}

function renderSessions() {
  if (!els.sessionList) return;
  if (els.sessionList.querySelector(".session-title-input")) return;
  els.sessionList.innerHTML = "";
  const sessionsList = state.sessions || [];
  if (!sessionsList.length) {
    els.sessionList.innerHTML = `<div class="empty">No sessions yet</div>`;
    return;
  }

  const query = (state.sessionSearchQuery || "").toLowerCase().trim();
  let filtered = sessionsList;
  if (query) {
    filtered = filtered.filter(sess =>
      (sess.title || "Untitled").toLowerCase().includes(query)
    );
  }

  if (filtered.length === 0) {
    els.sessionList.innerHTML = `<div class="empty">No matching sessions</div>`;
    return;
  }

  for (const sess of filtered) {
    const btn = document.createElement("button");
    btn.className = `session-item ${sess.id === state.activeSessionId ? "active" : ""}`;
    btn.dataset.id = sess.id;
    const fullDate = sess.updated_at ? new Date(sess.updated_at).toLocaleString() : "";
    const relativeDate = sess.updated_at ? formatRelativeTime(sess.updated_at) : "";
    btn.innerHTML = `<div class="session-info"><div class="session-title-row"><span class="session-title">${escapeHtml(sess.title || "Untitled")}</span></div><span class="session-meta" title="${escapeAttr(fullDate)}">${escapeHtml(relativeDate)}</span></div><div class="session-actions"><button class="session-rename-btn" type="button" title="Rename session">✏️</button><button class="session-delete" type="button" title="Delete session">×</button></div>`;
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
    state.sessions = (state.sessions || []).filter((s) => s.id !== id);
    if (state.activeSessionId === id) {
      state.activeSessionId = null;
      state.messages = [];
      state.attachments = [];
      renderMessages();
      renderAttachments();
      renderSessions();
      if (state.sessions.length > 0) {
        await loadSession(state.sessions[0].id);
      } else {
        await ensureActiveSession();
      }
      // Defensive fallback: if we still have no active session, force-create one
      if (!state.activeSessionId) {
        await createClientSession();
      }
    } else {
      renderSessions();
    }
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

function isDefaultTitle(title) {
  if (!title) return true;
  title = title.trim();
  if (title === "" || title === "New session" || title === "Empty Session") {
    return true;
  }
  // Check for Telegram Chat (chatID)
  if (title.startsWith("Telegram Chat (") && title.endsWith(")")) {
    const numPart = title.slice("Telegram Chat (".length, -1);
    if (!isNaN(numPart) && !isNaN(parseFloat(numPart))) {
      return true;
    }
  }
  return false;
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
    state.projects = await res.json() || [];
    renderProjectsList();
  } catch (err) {
    els.projectsList.innerHTML = `<div class="empty error">Error: ${err.message}</div>`;
  }
}

function renderProjectsList() {
  els.projectsList.innerHTML = "";
  if (!state.projects || state.projects.length === 0) {
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
  
  // Horizontal rules
  html = html.replace(/^\s*(\*{3,}|-{3,}|_{3,})\s*$/gm, "<hr>");

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
    showToast(`Could not create project: ${err.message}`, "error");
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
    showToast(err.message, "error");
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
    showToast(`Heartbeat tick failed: ${err.message}`, "error");
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
  const okDelete = await showConfirm({
    title: "Delete Project?",
    message: "Are you sure you want to delete this autonomous project permanently? All code files in its workspace directory and all execution logs will be erased.",
    okLabel: "Delete Project",
    danger: true,
  });
  if (!okDelete) return;
  
  try {
    const res = await fetch(`/api/autonomous/projects/${state.activeProjectId}`, {
      method: "DELETE"
    });
    if (!res.ok) throw new Error("Could not delete project");
    
    state.activeProjectId = null;
    await loadProjects();
    switchProjectsState("welcome");
  } catch (err) {
    showToast(err.message, "error");
  }
});

els.backToDetailBtn.addEventListener("click", () => {
  if (state.activeProjectId) {
    selectProject(state.activeProjectId);
  } else {
    switchProjectsState("welcome");
  }
});


// --- SEMANTIC MEMORY / RAG EXPLORER ---

async function openMemoryExplorer() {
  els.memoryDialog.showModal();
  els.memorySearch.value = "";
  els.reindexStatusArea.style.display = "none";
  await loadAndRenderMemories();
}

async function loadAndRenderMemories(searchResults = null) {
  try {
    els.currentEmbeddingModel.textContent = `Embedding Model: ${state.settings.model_embeddings || "None"}`;

    let entries = [];
    if (searchResults) {
      entries = searchResults;
    } else {
      const res = await fetch("/api/memory");
      if (!res.ok) throw new Error("Failed to fetch memories");
      const data = await res.json();
      entries = data.entries || [];
      els.memoryCount.textContent = `Total Entries: ${data.count || 0}`;
    }

    els.memoryListBody.innerHTML = "";
    if (entries.length === 0) {
      els.memoryListBody.innerHTML = `
        <tr>
          <td colspan="5" style="text-align: center; padding: 20px; color: var(--muted);">
            No memory entries found.
          </td>
        </tr>
      `;
      return;
    }

    entries.forEach(e => {
      const hasScore = typeof e.score === "number";
      const scoreVal = hasScore ? e.score : null;
      const entryData = e;
      const id = entryData.id;
      const text = entryData.text || "";
      const source = entryData.source || "user";
      const createdAt = entryData.created_at ? new Date(entryData.created_at).toLocaleString() : "";
      
      const scoreBadge = hasScore
        ? `<span class="score-badge" style="background: ${getScoreBg(scoreVal)}; color: #1e1e1e; padding: 2px 6px; border-radius: 4px; font-weight: bold; font-size: 11px;">${scoreVal.toFixed(4)}</span>`
        : `<span style="color: var(--muted);">-</span>`;

      const preview = text.length > 100 ? text.slice(0, 100) + "…" : text;
      const tr = document.createElement("tr");
      tr.style.borderBottom = "1px solid var(--line)";
      tr.style.height = "36px";
      tr.innerHTML = `
        <td style="padding: 8px 10px; max-width: 250px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap;">
          <span class="memory-text-preview" data-full="${escapeAttr(text)}" style="cursor: pointer; text-decoration: underline dotted; text-underline-offset: 3px;" title="Click to read full text">${escapeHtml(preview)}</span>
        </td>
        <td style="padding: 8px 10px; color: var(--muted);">${escapeHtml(source)}</td>
        <td style="padding: 8px 10px; color: var(--muted); font-size: 11.5px;">${createdAt}</td>
        <td style="padding: 8px 10px; text-align: center;">${scoreBadge}</td>
        <td style="padding: 8px 10px; text-align: right;">
          <button class="ghost-button delete-memory-btn" data-id="${id}" style="color: #ff6b6b; border-color: rgba(255,107,107,0.3); font-size: 11px; padding: 2px 8px;" type="button">Delete</button>
        </td>
      `;
      els.memoryListBody.appendChild(tr);
    });

    // Bind text preview click handlers
    els.memoryListBody.querySelectorAll(".memory-text-preview").forEach(span => {
      span.addEventListener("click", () => {
        els.memoryTextDialogContent.textContent = span.dataset.full || "";
        els.memoryTextDialog.showModal();
      });
    });

    // Bind delete handlers
    els.memoryListBody.querySelectorAll(".delete-memory-btn").forEach(btn => {
      btn.addEventListener("click", async () => {
        const id = btn.dataset.id;
        const okDel = await showConfirm({
          title: "Delete Memory Entry?",
          message: "Are you sure you want to delete this memory entry? This action cannot be undone.",
          okLabel: "Delete Entry",
          danger: true,
        });
        if (!okDel) return;
        try {
          const res = await fetch(`/api/memory/${encodeURIComponent(id)}`, { method: "DELETE" });
          if (!res.ok) throw new Error("Could not delete memory entry");
          await loadAndRenderMemories();
        } catch (err) {
          showToast(`Error: ${err.message}`, "error");
        }
      });
    });

  } catch (err) {
    console.error(err);
    els.memoryListBody.innerHTML = `
      <tr>
        <td colspan="5" style="text-align: center; padding: 20px; color: #ff6b6b;">
          Error loading memories: ${err.message}
        </td>
      </tr>
    `;
  }
}

function getScoreBg(score) {
  if (score >= 0.8) return "#4ade80"; // green
  if (score >= 0.5) return "#facc15"; // yellow
  return "#f87171"; // red
}

// Bind search and test RAG query
els.testSearchBtn.addEventListener("click", async () => {
  const query = els.memorySearch.value.trim();
  if (!query) {
    // Reset list
    await loadAndRenderMemories();
    return;
  }

  try {
    els.testSearchBtn.disabled = true;
    els.testSearchBtn.textContent = "Searching...";
    const res = await fetch("/api/memory/search", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ query: query, top_k: 10 })
    });
    if (!res.ok) throw new Error("Similarity search failed");
    const data = await res.json();
    await loadAndRenderMemories(data.results || []);
  } catch (err) {
    showToast(`RAG Search Error: ${err.message}`, "error");
  } finally {
    els.testSearchBtn.disabled = false;
    els.testSearchBtn.textContent = "Test RAG Search";
  }
});

// Bind re-indexing manual action
els.reindexMemoryBtn.addEventListener("click", async () => {
  const okReindex = await showConfirm({
    title: "Re-index Memory?",
    message: "This will re-index all memory entries using the current embedding model. Continue?",
    okLabel: "Re-index",
    danger: false,
  });
  if (!okReindex) return;
  try {
    els.reindexMemoryBtn.disabled = true;
    els.reindexStatusArea.style.display = "block";
    els.reindexStatusArea.querySelector(".status-text").textContent = "Re-indexing memories sequentially on Ollama...";

    const res = await fetch("/api/memory/reindex", { method: "POST" });
    const data = await res.json();
    if (!res.ok) throw new Error(data.error || "Reindexing failed");

    showToast(`Re-indexed ${data.count} memory entries using model: ${data.model}`, "success");
    await loadAndRenderMemories();
  } catch (err) {
    showToast(`Reindexing Error: ${err.message}`, "error");
  } finally {
    els.reindexStatusArea.style.display = "none";
    els.reindexMemoryBtn.disabled = false;
  }
});

// Password visibility toggler
document.addEventListener("click", (e) => {
  const toggleBtn = e.target.closest(".password-toggle-btn");
  if (!toggleBtn) return;
  
  const wrapper = toggleBtn.closest(".password-input-wrapper");
  if (!wrapper) return;
  
  const input = wrapper.querySelector("input");
  if (!input) return;
  
  if (input.type === "password") {
    input.type = "text";
    toggleBtn.textContent = "🙈";
  } else {
    input.type = "password";
    toggleBtn.textContent = "👁️";
  }
});




