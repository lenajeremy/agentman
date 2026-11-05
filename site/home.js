// The home page's motion: smooth scrolling (Lenis), headlines that rise
// line by line, blocks that come in as they scroll into view, and a few
// things tied to the scroll itself. With reduced motion asked for, none of
// it runs and the page is simply there.
(() => {
  const root = document.documentElement;
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
