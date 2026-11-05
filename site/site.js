// The site's script. Copy buttons on every page; then the motion: smooth
// scrolling (Lenis), headlines that rise line by line, blocks that come in
// as they scroll into view, and a few things tied to the scroll itself.
// With reduced motion asked for, none of the motion runs and the page is
// simply there.
(() => {
  // The install command names the address this page was served from, so it
  // stays right whichever domain the site is on.
  if (location.protocol === "https:") {
    for (const block of document.querySelectorAll("[data-origin-cmd]")) {
      const swap = (text) => text.replace(/https:\/\/[^/\s]+\/install/, `${location.origin}/install`);
      const code = block.querySelector("code");
      const button = block.querySelector("[data-copy]");
      code.lastChild.textContent = swap(code.lastChild.textContent);
      button.dataset.copy = swap(button.dataset.copy);
    }
  }

  for (const button of document.querySelectorAll("[data-copy]")) {
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
      button.classList.add("done");
      setTimeout(() => {
        label.textContent = "Copy";
        button.classList.remove("done");
      }, 1600);
    });
  }

  const root = document.documentElement;

  // A section marked data-zone colours the whole page while it holds the
  // middle of the view.
  const zones = new Map();
  const zoneWatch = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) zones.set(entry.target, entry.isIntersecting);
      const on = [...zones].find(([, inView]) => inView);
      if (on) root.dataset.zone = on[0].dataset.zone;
      else delete root.dataset.zone;
    },
    { rootMargin: "-45% 0px -45% 0px" },
  );
  for (const zone of document.querySelectorAll("[data-zone]")) zoneWatch.observe(zone);

  if (!root.classList.contains("motion")) return;

  // Headlines: each line in a mask of its own, rising in turn.
  for (const heading of document.querySelectorAll("[data-split]")) {
    const lines = heading.innerHTML.split(/<br\s*\/?>/i);
    heading.innerHTML = lines
      .map((line, i) => `<span class="line"><span style="--i:${i}">${line.trim()}</span></span>`)
      .join("");
    heading.setAttribute("data-reveal", "lines");
  }

  // Everything marked comes in once, the first time it is mostly in view.
  const seen = new IntersectionObserver(
    (entries) => {
      for (const entry of entries) {
        if (!entry.isIntersecting) continue;
        entry.target.classList.add("in");
        seen.unobserve(entry.target);
      }
    },
    { rootMargin: "0px 0px -12% 0px", threshold: 0.12 },
  );
  for (const el of document.querySelectorAll("[data-reveal]")) seen.observe(el);

  // What moves with the scroll: the hero shot lies back and comes flat, the
  // phone rises in its tile, the badge turns.
  const tilt = document.querySelector("[data-tilt]");
  const parallax = [...document.querySelectorAll("[data-parallax]")];
  const ring = document.querySelector(".badge-ring");
  const drift = [...document.querySelectorAll("[data-drift]")];
  const clamp = (v) => Math.min(1, Math.max(0, v));
  const update = (y) => {
    const vh = window.innerHeight;
    if (tilt) {
      const box = tilt.getBoundingClientRect();
      tilt.style.setProperty("--p", clamp((vh - box.top) / (vh * 0.9)).toFixed(3));
    }
    for (const el of parallax) {
      const box = el.getBoundingClientRect();
      el.style.setProperty("--p", clamp((vh - box.top) / (vh + box.height)).toFixed(3));
    }
    if (ring) ring.style.setProperty("--turn", `${(y * 0.12).toFixed(1)}deg`);
    // outlined lines slide across as they pass through the view
    for (const el of drift) {
      const box = el.parentElement.getBoundingClientRect();
      const p = clamp((vh - box.top) / (vh + box.height));
      const dir = Number(el.dataset.driftDir || 1);
      el.style.setProperty("--x", `${((0.5 - p) * 30 * dir - (dir > 0 ? 12 : 0)).toFixed(2)}%`);
    }
  };

  let lenis = null;
  if (window.Lenis) {
    lenis = new window.Lenis({ autoRaf: true, anchors: { offset: -16 }, lerp: 0.11 });
    lenis.on("scroll", ({ scroll }) => update(scroll));
  } else {
    window.addEventListener("scroll", () => update(window.scrollY), { passive: true });
  }
  window.addEventListener("resize", () => update(window.scrollY));
  update(window.scrollY);
})();
