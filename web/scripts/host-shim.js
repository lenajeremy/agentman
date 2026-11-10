// Loaded by the editor page (public/editor) before its own script. The page
// was written for WKWebView: it posts to webkit.messageHandlers.editor and
// asks webkit.messageHandlers.editorAsk, and Swift calls window.agentman.
// Here the page sits in an iframe, so both directions go through
// postMessage to the app's window, which answers for Swift
// (src/editor/monaco.ts).
;(function () {
  var pending = new Map()
  var next = 0
  // Monaco drops work it no longer needs (a hover, a highlight, a model
  // closed) by rejecting with an error named Canceled, which VS Code's own
  // handler ignores; Safari reports each as an unhandled rejection. Ignored
  // here the same way, and nothing else.
  window.addEventListener('unhandledrejection', function (e) {
    var reason = e.reason
    if (reason && (reason.name === 'Canceled' || reason.message === 'Canceled')) e.preventDefault()
  })
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
      var result
      try {
        result = fn ? fn.apply(window.agentman, data.args || []) : undefined
      } catch (error) {
        window.parent.postMessage({ channel: 'editor', body: { type: 'log', message: data.name + ': ' + error } }, '*')
      }
      Promise.resolve(result).then(function (value) {
        window.parent.postMessage({ channel: 'editorReturn', id: data.id, result: value === undefined ? null : value }, '*')
      })
    } else if (data.channel === 'editorClipboard') {
      // Cut and Copy from the app's menu, as the Edit menu's would.
      document.execCommand(data.action)
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

  // The app's keys (the page's isAppKey: Save, Go to File, the terminal,
  // text size…) never reach Monaco; in WKWebView they go on to the menu bar.
  // Here they go to the app's window, which answers them as its menus do.
  // Off the Mac, Ctrl is the command key, as Monaco takes it there.
  var mac = /Mac/.test(navigator.platform)
  function isAppKey(e) {
    var key = (e.key || '').toLowerCase()
    if (e.ctrlKey && !e.metaKey && (key === '`' || e.code === 'Backquote')) return true
    var command = mac ? e.metaKey && !e.ctrlKey : e.ctrlKey && !e.metaKey
    if (!mac && command && key === 'w' && document.querySelector('.vim')) return false
    if (mac && e.ctrlKey && !e.metaKey) return false
    if (!command) return false
    if (e.shiftKey && !e.altKey) return ['e', 'f', 'v'].indexOf(key) >= 0
    return ['s', 'p', 'k', 'n', 'o', 'w', 'l', ',', '=', '+', '-', '0', '\\'].indexOf(key) >= 0
  }
  window.addEventListener(
    'keydown',
    function (e) {
      if (!isAppKey(e)) return
      e.preventDefault()
      window.parent.postMessage(
        {
          channel: 'editorKey',
          key: e.key, code: e.code, metaKey: e.metaKey, ctrlKey: e.ctrlKey, shiftKey: e.shiftKey, altKey: e.altKey,
        },
        '*',
      )
      // A key that opens a palette over the code (⌘K, ⌘P, ⇧⌘F): the code
      // lets the keyboard go at once. The palette is drawn by the window a
      // moment later, and what was typed in that moment went into the file.
      var key = (e.key || '').toLowerCase()
      if ((!e.shiftKey && (key === 'k' || key === 'p')) || (e.shiftKey && key === 'f')) {
        var active = document.activeElement
        if (active && active.blur) active.blur()
      }
    },
    true,
  )
})()
