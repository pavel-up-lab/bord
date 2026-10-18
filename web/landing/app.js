(function () {
  "use strict";

  const config = window.BORD_LANDING_CONFIG || {};
  const state = { os: detectOS(), architecture: detectArchitecture() };
  const osLabels = { linux: "Linux", darwin: "macOS", windows: "Windows" };

  const tabs = Array.from(document.querySelectorAll("[data-os]"));
  const architecture = document.querySelector("#architecture");
  const downloadName = document.querySelector("[data-download-name]");
  const downloadLink = document.querySelector("[data-download-link]");
  const downloadState = document.querySelector("[data-download-state]");
  const installCommand = document.querySelector("[data-install-command]");
  const runCommands = Array.from(document.querySelectorAll("[data-run-command]"));
  const boardState = document.querySelector("[data-board-state]");
  const toast = document.querySelector("[data-toast]");
  let toastTimer;

  function detectOS() {
    const platform = (navigator.userAgentData && navigator.userAgentData.platform) || navigator.platform || "";
    if (/Win/i.test(platform)) return "windows";
    if (/Mac/i.test(platform)) return "darwin";
    return "linux";
  }

  function detectArchitecture() {
    const source = `${navigator.userAgent || ""} ${navigator.platform || ""}`;
    return /arm64|aarch64|Macintosh.*Apple/i.test(source) ? "arm64" : "amd64";
  }

  function cleanBase(value) {
    const candidate = String(value || "").trim();
    if (!candidate) return "";
    try {
      const parsed = new URL(candidate);
      if (parsed.protocol !== "https:") return "";
      return parsed.href.replace(/\/+$/, "");
    } catch (_) {
      return "";
    }
  }

  function archiveName() {
    const suffix = state.os === "windows" ? ".zip" : ".tar.gz";
    return `bord_${config.version || "VERSION"}_${state.os}_${state.architecture}${suffix}`;
  }

  function installText(name, base) {
    const url = `${base || "<DOWNLOAD_BASE_URL>"}/${name}`;
    const root = name.replace(/\.(?:tar\.gz|zip)$/, "");
    if (state.os === "windows") {
      return `Invoke-WebRequest -Uri "${url}" -OutFile "${name}"\nExpand-Archive .\\${name} -DestinationPath .\\bord -Force\n.\\bord\\${root}\\bord.exe --version`;
    }
    return `curl -fLO "${url}"\ntar -xzf "${name}"\nsudo install -m 0755 "${root}/bord" /usr/local/bin/bord`;
  }

  function runText(boardURL) {
    const server = boardURL || "<BOARD_URL>";
    if (state.os === "windows") {
      const root = archiveName().replace(/\.zip$/, "");
      return `.\\bord\\${root}\\bord.exe --server ${server}`;
    }
    return `bord --server ${server}`;
  }

  function update() {
    const name = archiveName();
    const downloadBase = cleanBase(config.downloadBaseURL);
    const boardURL = cleanBase(config.boardURL);

    tabs.forEach((tab) => tab.setAttribute("aria-selected", String(tab.dataset.os === state.os)));
    architecture.value = state.architecture;
    downloadName.textContent = name;
    installCommand.textContent = installText(name, downloadBase);

    if (downloadBase) {
      downloadLink.href = `${downloadBase}/${name}`;
      downloadLink.classList.remove("disabled");
      downloadLink.removeAttribute("aria-disabled");
      downloadState.textContent = `Готовая сборка: ${osLabels[state.os]} ${state.architecture.toUpperCase()}`;
    } else {
      downloadLink.removeAttribute("href");
      downloadLink.classList.add("disabled");
      downloadLink.setAttribute("aria-disabled", "true");
      downloadState.textContent = "Download-домен ещё не настроен";
    }

    const command = runText(boardURL);
    runCommands.forEach((element) => { element.textContent = command; });
    boardState.textContent = boardURL ? `Сервер: ${boardURL}` : "Адрес борды ещё не настроен";
  }

  async function copy(text) {
    try {
      await navigator.clipboard.writeText(text);
    } catch (_) {
      const area = document.createElement("textarea");
      area.value = text;
      area.setAttribute("readonly", "");
      area.style.position = "fixed";
      area.style.opacity = "0";
      document.body.appendChild(area);
      area.select();
      document.execCommand("copy");
      area.remove();
    }
    clearTimeout(toastTimer);
    toast.classList.add("visible");
    toastTimer = setTimeout(() => toast.classList.remove("visible"), 1800);
  }

  tabs.forEach((tab) => tab.addEventListener("click", () => {
    state.os = tab.dataset.os;
    update();
  }));
  architecture.addEventListener("change", () => {
    state.architecture = architecture.value;
    update();
  });
  document.querySelectorAll("[data-copy-install]").forEach((button) => {
    button.addEventListener("click", () => copy(installCommand.textContent));
  });
  document.querySelectorAll("[data-copy-run]").forEach((button) => {
    button.addEventListener("click", () => copy(runCommands[0].textContent));
  });
  document.querySelectorAll("[data-year]").forEach((element) => {
    element.textContent = String(new Date().getFullYear());
  });

  update();
})();
