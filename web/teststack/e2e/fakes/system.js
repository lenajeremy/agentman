// The system am serve says it runs on (Host.System), as window.__system,
// set before the page's scripts: what a page sees where am serve isn't on a
// Mac.
{
  const F = window.fetch;
  window.fetch = (input, init) => (String(input.url ?? input).endsWith("/app/host/Host.System")
    ? Promise.resolve(new Response(JSON.stringify({ result: window.__system }), { status: 200, headers: { "Content-Type": "application/json" } }))
    : F(input, init));
}
