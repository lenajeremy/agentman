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

  // The fleet: one dot per agent on your Mac. Agents drift between working
  // and idle; every so often one stops to ask a question and turns orange,
  // and a tap — the visitor's, or a demo finger's — sends it back to work.
  const dots = document.querySelector("[data-dots]");
  if (dots) {
    const names = [
      "api-migrations", "fix-auth-refresh", "web-checkout", "docs-refresh", "search-index",
      "billing-hooks", "ios-onboarding", "perf-budget", "e2e-flakes", "infra-plan",
      "design-tokens", "data-backfill", "auth-sso", "queue-retries", "landing-copy",
      "mobile-push", "cli-release",
    ];
    const labels = { "": "idle", c: "working", t: "needs you" };
    const fleet = names.map((name) => {
      const dot = document.createElement("i");
      dot.dataset.name = name;
      dots.appendChild(dot);
      return dot;
    });
    const setState = (dot, state, pop) => {
      dot.className = state;
      dot.dataset.state = labels[state];
      if (pop && !reduceMotion) {
        void dot.offsetWidth; // restart the animation if it is already running
        dot.classList.add("pop");
      }
    };
    [..."..cc.c..c..c.c..."].forEach((kind, i) => setState(fleet[i], kind === "c" ? "c" : "", false));

    const caption = document.querySelector("[data-dotcap]");
    const idleCaption = caption ? caption.innerHTML : "";
    const say = (html) => {
      if (!caption) return;
      caption.classList.add("swap");
      setTimeout(() => {
        caption.innerHTML = html;
        caption.classList.remove("swap");
      }, 180);
    };

    if (reduceMotion) {
      // A still page still tells the whole story.
      setState(fleet[8], "t", false);
      if (caption) caption.innerHTML = `<b class="ask">${names[8]}</b> is waiting on you. The rest are working or idle.`;
    } else {
      const finger = document.querySelector("[data-finger]");
      const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
      let visible = false;
      let answered = null; // resolves when the orange dot is tapped

      // Only animate while someone can see it.
      new IntersectionObserver(([entry]) => { visible = entry.isIntersecting; }, { threshold: .3 })
        .observe(dots);
      const waitUntilVisible = async () => {
        while (!visible || document.hidden) await sleep(400);
      };

      // Visitors can answer the agent themselves.
      dots.addEventListener("click", (event) => {
        if (event.target instanceof HTMLElement && event.target.classList.contains("t") && answered) {
          answered(event.target);
        }
      });

      setInterval(() => {
        if (!visible || document.hidden) return;
        const calm = fleet.filter((dot) => !dot.classList.contains("t"));
        const dot = calm[Math.floor(Math.random() * calm.length)];
        const working = fleet.filter((d) => d.classList.contains("c")).length;
        const next = dot.classList.contains("c") ? (working > 4 ? "" : "c") : (working < 8 ? "c" : "");
        setState(dot, next, true);
      }, 1100);

      const tapWithFinger = async (dot) => {
        if (!finger) return;
        const box = dot.getBoundingClientRect();
        const origin = dots.getBoundingClientRect();
        const x = box.left - origin.left, y = box.top - origin.top;
        finger.style.transition = "none";
        finger.style.setProperty("--at", `translate(${x + 60}px, ${y + 70}px)`);
        finger.style.transform = "var(--at)";
        void finger.offsetWidth;
        finger.style.transition = "";
        finger.style.opacity = "1";
        finger.style.setProperty("--at", `translate(${x}px, ${y}px)`);
        await sleep(780);
        finger.classList.remove("press");
        void finger.offsetWidth;
        finger.classList.add("press");
        await sleep(160);
      };

      (async () => {
        for (let turn = 0; ; turn++) {
          await waitUntilVisible();
          await sleep(turn === 0 ? 1600 : 3800);
          await waitUntilVisible();

          // Someone who was working stops to ask.
          const candidates = fleet.filter((dot, i) => dot.classList.contains("c") && i > 1 && i < fleet.length - 2);
          const dot = candidates[Math.floor(Math.random() * candidates.length)] || fleet[8];
          setState(dot, "t", true);
          say(`<b class="ask">${dot.dataset.name}</b> stopped to ask you something.`);

          // Give the visitor a moment to tap it themselves.
          const tappedBy = await Promise.race([
            new Promise((resolve) => { answered = () => resolve("visitor"); }),
            sleep(2600).then(() => "demo"),
          ]);
          answered = null;
          if (tappedBy === "demo") await tapWithFinger(dot);

          setState(dot, "c", true);
          dot.classList.add("tapped");
          setTimeout(() => dot.classList.remove("tapped"), 650);
          say(tappedBy === "visitor"
            ? `Nice. <b class="done">${dot.dataset.name}</b> has its answer and is back to work.`
            : `Answered from the phone. <b class="done">${dot.dataset.name}</b> is back to work.`);
          if (finger) {
            await sleep(260);
            finger.style.opacity = "0";
          }
          await sleep(2600);
          say(idleCaption);
        }
      })();
    }
  }

  // Sections rise into place as they scroll into view. Marking the page
  // first means nothing is hidden when this script never runs.
  if (!reduceMotion && "IntersectionObserver" in window) {
    document.documentElement.classList.add("js");
    const groups = [
      ".why .h2", ".why > p", ".cards .card", ".stats > div", ".how .caps", ".how .h2",
      ".steps .step", ".moment li", ".moment .h2", ".start .h2", ".platform",
    ];
    const reveal = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        const element = entry.target;
        element.classList.add("in");
        reveal.unobserve(element);
        // Once it has arrived, hand the element its own transitions back, so
        // hover effects run at their own speed rather than the reveal's.
        const delay = Number(element.style.getPropertyValue("--i") || 0) * 70;
        setTimeout(() => {
          delete element.dataset.reveal;
          element.classList.remove("in");
        }, 900 + delay);
      }
    }, { threshold: .15, rootMargin: "0px 0px -40px 0px" });
    for (const selector of groups) {
      document.querySelectorAll(selector).forEach((element, i) => {
        element.dataset.reveal = "";
        element.style.setProperty("--i", String(i % 8));
        reveal.observe(element);
      });
    }
  }

  // The stats count up the first time they are seen.
  const counters = document.querySelectorAll("[data-count]");
  if (counters.length && !reduceMotion && "IntersectionObserver" in window) {
    const count = new IntersectionObserver((entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        count.unobserve(entry.target);
        const element = entry.target;
        const target = Number(element.dataset.count);
        const suffix = element.dataset.suffix || "";
        const started = performance.now();
        const step = (now) => {
          const t = Math.min(1, (now - started) / 900);
          const eased = 1 - Math.pow(1 - t, 3);
          element.textContent = Math.round(target * eased) + suffix;
          if (t < 1) requestAnimationFrame(step);
          else element.classList.add("pop");
        };
        requestAnimationFrame(step);
      }
    }, { threshold: .6 });
    counters.forEach((element) => count.observe(element));
  }

  // How it works: three steps, each with its own scene. They play in turn
  // while the section is on screen, pause while hovered, and any step can be
  // picked directly.
  const stepsPanel = document.querySelector("[data-steps]");
  const stepList = document.querySelector(".steps");
  if (stepsPanel && stepList) {
    const stepTabs = Array.from(stepList.querySelectorAll("[role=tab]"));
    const scenes = stepTabs.map((tab) => document.getElementById(tab.getAttribute("aria-controls")));
    // Each step can ask for longer; the phone's story has more to show.
    const durationOf = (index) => Number(stepTabs[index].dataset.ms) || 6500;
    let current = 0;
    let timer = null;
    let startedAt = 0;
    let remaining = durationOf(0);
    let onScreen = false;
    let hovering = false;

    const show = (index, focus) => {
      current = index;
      stepTabs.forEach((tab, i) => {
        const on = i === index;
        tab.classList.toggle("is-on", on);
        tab.setAttribute("aria-selected", String(on));
        tab.tabIndex = on ? 0 : -1;
        scenes[i].classList.toggle("is-on", on);
        scenes[i].hidden = !on;
        if (on && focus) tab.focus();
      });
      // Restart the scene's own animations and the progress rule.
      const scene = scenes[index];
      scene.classList.remove("is-on");
      void scene.offsetWidth;
      scene.classList.add("is-on");
      stepList.classList.remove("playing");
      void stepList.offsetWidth;
      remaining = durationOf(index);
      stepList.style.setProperty("--step-ms", `${remaining}ms`);
      run();
    };
    const run = () => {
      clearTimeout(timer);
      const playing = !reduceMotion && onScreen && !hovering && !document.hidden;
      stepList.classList.toggle("playing", !reduceMotion && onScreen);
      stepList.classList.toggle("paused", !playing);
      if (!playing) return;
      startedAt = performance.now();
      timer = setTimeout(() => show((current + 1) % stepTabs.length, false), remaining);
    };
    const pause = () => {
      if (timer) remaining = Math.max(0, remaining - (performance.now() - startedAt));
      clearTimeout(timer);
      timer = null;
      run();
    };

    stepTabs.forEach((tab, i) => {
      tab.addEventListener("click", () => show(i, false));
      tab.addEventListener("keydown", (event) => {
        const step = event.key === "ArrowRight" ? 1 : event.key === "ArrowLeft" ? -1 : 0;
        if (!step) return;
        event.preventDefault();
        show((i + step + stepTabs.length) % stepTabs.length, true);
      });
    });
    for (const area of [stepsPanel, stepList]) {
      area.addEventListener("mouseenter", () => { hovering = true; pause(); });
      area.addEventListener("mouseleave", () => { hovering = false; run(); });
    }
    document.addEventListener("visibilitychange", () => (document.hidden ? pause() : run()));
    if ("IntersectionObserver" in window) {
      new IntersectionObserver(([entry]) => {
        const wasOnScreen = onScreen;
        onScreen = entry.isIntersecting;
        if (onScreen && !wasOnScreen) show(current, false);
        else if (!onScreen) pause();
      }, { threshold: .35 }).observe(stepsPanel);
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
        button.classList.remove("done");
        void button.offsetWidth;
        button.classList.add("done");
      } catch {
        const range = document.createRange();
        range.selectNodeContents(button.previousElementSibling);
        const selection = window.getSelection();
        selection.removeAllRanges();
        selection.addRange(range);
        label.textContent = "Selected";
      }
      setTimeout(() => {
        label.textContent = "Copy";
        button.classList.remove("done");
      }, 1600);
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
