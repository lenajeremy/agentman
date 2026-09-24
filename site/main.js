(() => {
  const reduceMotion = window.matchMedia("(prefers-reduced-motion: reduce)").matches;

  // The headline cycles through the agents Agentman supports.
  const rotor = document.querySelector("[data-rotor]");
  const pills = Array.from(document.querySelectorAll("[data-pills] span"));
  const agents = ["Claude Code", "Codex", "OpenCode"];
  if (rotor && !reduceMotion) {
    let index = 0;
    setInterval(() => {
      index = (index + 1) % agents.length;
      rotor.classList.add("out");
      setTimeout(() => {
        rotor.textContent = agents[index];
        rotor.classList.remove("out");
        pills.forEach((pill, i) => pill.classList.toggle("on", i === index));
      }, 250);
    }, 2800);
  }

  // One dot per agent: most working, one waiting on you.
  const dots = document.querySelector("[data-dots]");
  if (dots) {
    for (const kind of "..cc.c..t..c.c...") {
      const dot = document.createElement("i");
      if (kind === "c") dot.className = "c";
      if (kind === "t") dot.className = "t";
      dots.appendChild(dot);
    }
  }

  // Platform tabs in "Get started".
  const tabs = Array.from(document.querySelectorAll("[role=tab][data-platform]"));
  function select(platform, focus) {
    for (const tab of tabs) {
      const on = tab.dataset.platform === platform;
      tab.setAttribute("aria-selected", String(on));
      tab.tabIndex = on ? 0 : -1;
      document.getElementById(tab.getAttribute("aria-controls")).hidden = !on;
      if (on && focus) tab.focus();
    }
  }
  tabs.forEach((tab, i) => {
    tab.addEventListener("click", () => select(tab.dataset.platform, false));
    tab.addEventListener("keydown", (event) => {
      const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
      if (!step) return;
      event.preventDefault();
      select(tabs[(i + step + tabs.length) % tabs.length].dataset.platform, true);
    });
  });
  document.querySelectorAll("[data-platform-link]").forEach((link) => {
    link.addEventListener("click", () => select(link.dataset.platformLink, false));
  });

  // Copy buttons.
  document.querySelectorAll("[data-copy]").forEach((button) => {
    const label = button.querySelector("span");
    button.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(button.dataset.copy);
        label.textContent = "Copied";
      } catch {
        const range = document.createRange();
        range.selectNodeContents(button.previousElementSibling);
        const selection = window.getSelection();
        selection.removeAllRanges();
        selection.addRange(range);
        label.textContent = "Selected";
      }
      setTimeout(() => (label.textContent = "Copy"), 1600);
    });
  });

  const withTimeout = (url, ms) => {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), ms);
    return fetch(url, { signal: controller.signal }).finally(() => clearTimeout(timer));
  };

  // Star count, when GitHub answers.
  const stars = document.querySelector("[data-stars]");
  if (stars) {
    withTimeout("https://api.github.com/repos/lenajeremy/agentman", 5000)
      .then((response) => (response.ok ? response.json() : null))
      .then((repo) => {
        if (repo && typeof repo.stargazers_count === "number") {
          stars.textContent = repo.stargazers_count.toLocaleString();
        }
      })
      .catch(() => {});
  }

  // Live status of the public relay, which exposes a CORS-enabled /health.
  const relay = document.querySelector("[data-relay]");
  if (relay) {
    const label = relay.querySelector("span");
    withTimeout("https://agentman-production.up.railway.app/health", 5000)
      .then((response) => (response.ok ? response.json() : null))
      .then((health) => {
        if (health && health.status === "ok") {
          relay.classList.add("online");
          label.textContent = "Public relay online";
        } else {
          label.textContent = "Public relay unreachable";
        }
      })
      .catch(() => (label.textContent = "Public relay unreachable"));
  }
})();
