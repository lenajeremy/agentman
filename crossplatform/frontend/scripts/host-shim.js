// Loaded by the editor page (public/editor) before its own script. The page
// was written for WKWebView: it posts to webkit.messageHandlers.editor and
// asks webkit.messageHandlers.editorAsk, and Swift calls window.agentman.
// Here the page sits in an iframe, so both directions go through
// postMessage to the app's window, which answers for Swift (src/Editor.tsx).
;(function () {
  var pending = new Map()
  var next = 0
  window.addEventListener('message', function (e) {
    if (e.source !== window.parent) return
    var data = e.data || {}
    if (data.channel === 'editorAnswer') {
      var resolve = pending.get(data.id)
      if (resolve) {
        pending.delete(data.id)
        resolve(data.result)
      }
    } else if (data.channel === 'editorCall') {
      // The app calls window.agentman.<name>(...args), as Swift does.
      var fn = window.agentman && window.agentman[data.name]
      var result = fn ? fn.apply(window.agentman, data.args || []) : undefined
      Promise.resolve(result).then(function (value) {
        window.parent.postMessage({ channel: 'editorReturn', id: data.id, result: value === undefined ? null : value }, '*')
      })
    }
  })
  window.webkit = {
    messageHandlers: {
      editor: {
        postMessage: function (body) {
          window.parent.postMessage({ channel: 'editor', body: body }, '*')
        },
      },
      editorAsk: {
        postMessage: function (body) {
          var id = ++next
          return new Promise(function (resolve) {
            pending.set(id, resolve)
            window.parent.postMessage({ channel: 'editorAsk', id: id, body: body }, '*')
          })
        },
      },
    },
  }
})()
