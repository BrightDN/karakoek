(function () {
  var csrf = document.querySelector('meta[name="csrf-token"]').content;
  var body = document.getElementById('queue-body');
  var empty = document.getElementById('empty');
  var panel = document.getElementById('panel');
  var nowTitle = document.getElementById('now-title');
  var toast = document.getElementById('toast');
  var screenPill = document.getElementById('screen-status');
  var connPill = document.getElementById('conn');
  var bPlay = document.getElementById('b-play');
  var bPause = document.getElementById('b-pause');

  var current = null, state = 'cued', toastTimer;

  function showError(msg) {
    toast.textContent = msg; toast.hidden = false;
    clearTimeout(toastTimer);
    toastTimer = setTimeout(function () { toast.hidden = true; }, 5000);
  }

  var ws = connectWS('controller', {
    open: function () { connPill.textContent = 'Connected'; connPill.className = 'pill on'; },
    close: function () { connPill.textContent = 'Reconnecting…'; connPill.className = 'pill off'; },
    queue_updated: function (p) {
      current = p.current || null;
      if (p.state) state = p.state;
      render(p.queue || []);
    },
    player_state: function (p) { state = p.state; renderPanel(); },
    screen_status: function (p) {
      var on = p.connected > 0;
      screenPill.textContent = on ? 'Player screen connected' : 'No player screen';
      screenPill.className = 'pill ' + (on ? 'on' : 'off');
    },
    error: function (p) { showError(p.message || 'Error'); }
  });

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined) e.textContent = text;
    return e;
  }
  function cell(label, child) {
    var td = el('td'); td.dataset.label = label;
    if (typeof child === 'string') td.textContent = child; else if (child) td.appendChild(child);
    return td;
  }

  function api(method, url, data) {
    var opts = { method: method, headers: { 'X-CSRF-Token': csrf } };
    if (data) {
      opts.headers['Content-Type'] = 'application/x-www-form-urlencoded';
      opts.body = new URLSearchParams(data).toString();
    }
    return fetch(url, opts).then(function (r) {
      return r.json().catch(function () { return {}; }).then(function (j) {
        if (!r.ok) throw new Error(j.error || 'Request failed');
        return j;
      });
    }).catch(function (e) { showError(e.message); });
  }

  function render(queue) {
    body.textContent = '';
    queue.forEach(function (r) {
      var tr = el('tr', r.priority ? 'prio' : '');
      tr.appendChild(cell('Requester', r.requester));
      tr.appendChild(cell('Table', String(r.table)));
      tr.appendChild(cell('Priority', r.priority ? el('span', 'badge', '★ Priority') : ''));
      tr.appendChild(cell('Song', r.song));
      tr.appendChild(cell('Artist', r.artist));

      var linkCell;
      if (r.link) {
        var a = el('a', '', 'Open'); a.href = r.link; a.target = '_blank'; a.rel = 'noopener noreferrer';
        linkCell = a;
      } else {
        linkCell = el('form', 'inline');
        var inp = el('input'); inp.type = 'url'; inp.placeholder = 'YouTube link'; inp.required = true;
        var save = el('button', 'secondary', 'Save'); save.type = 'submit';
        linkCell.appendChild(inp); linkCell.appendChild(save);
        linkCell.addEventListener('submit', function (e) {
          e.preventDefault();
          api('POST', '/admin/requests/' + r.id + '/link', { link: inp.value });
        });
      }
      tr.appendChild(cell('Link', linkCell));

      var ctl = el('div', 'btns');
      var play = el('button', '', 'Play');
      play.disabled = !r.link || !!current;
      play.addEventListener('click', function () { ws.send('GetVideo', { requestId: r.id }); });
      var rm = el('button', 'danger', 'Remove');
      rm.addEventListener('click', function () {
        if (confirm('Remove "' + r.song + '" requested by ' + r.requester + '?')) api('DELETE', '/admin/requests/' + r.id);
      });
      ctl.appendChild(play); ctl.appendChild(rm);
      tr.appendChild(cell('Controls', ctl));
      body.appendChild(tr);
    });
    empty.hidden = queue.length > 0;
    renderPanel();
  }

  function renderPanel() {
    panel.hidden = !current;
    if (!current) return;
    nowTitle.textContent = current.song + ' — ' + current.artist + ' (' + current.requester + ')';
    bPlay.hidden = state === 'playing';
    bPause.hidden = state !== 'playing';
  }

  bPlay.addEventListener('click', function () { ws.send('PlayVideo'); });
  bPause.addEventListener('click', function () { ws.send('PauseVideo'); });
  document.getElementById('b-rewind').addEventListener('click', function () { ws.send('RestartVideo'); });
  document.getElementById('b-finish').addEventListener('click', function () {
    if (confirm('Mark this song as finished?')) ws.send('FinishVideo');
  });
  document.getElementById('b-undo').addEventListener('click', function () { ws.send('UndoVideo'); });
})();
