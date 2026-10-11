// Small reconnecting WebSocket helper shared by the two admin pages.
window.connectWS = function (role, handlers) {
  var backoff = 500, ws, closed = false;
  function open() {
    var proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    ws = new WebSocket(proto + '//' + location.host + '/ws?role=' + role);
    ws.onopen = function () { backoff = 500; handlers.open && handlers.open(); };
    ws.onmessage = function (ev) {
      var m; try { m = JSON.parse(ev.data); } catch (e) { return; }
      var h = handlers[m.type]; if (h) h(m.payload || {});
    };
    ws.onclose = function () {
      handlers.close && handlers.close();
      if (closed) return;
      setTimeout(open, backoff);
      backoff = Math.min(backoff * 2, 15000);
    };
  }
  open();
  return {
    send: function (type, payload) {
      if (ws && ws.readyState === 1) ws.send(JSON.stringify({ type: type, payload: payload || {} }));
    }
  };
};
