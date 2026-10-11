(function () {
  // Mandatory name dialog: cannot be dismissed.
  var dlg = document.getElementById('name-dialog');
  if (dlg && typeof dlg.showModal === 'function') {
    dlg.addEventListener('cancel', function (e) { e.preventDefault(); });
    dlg.showModal();
  }

  // Live cooldown countdown (server stays authoritative).
  var box = document.getElementById('cooldown');
  if (box) {
    var left = parseInt(box.dataset.remaining, 10) || 0;
    var out = document.getElementById('cd-time');
    var btn = document.getElementById('submit-btn');
    var end = Date.now() + left * 1000;
    var tick = function () {
      var s = Math.max(0, Math.ceil((end - Date.now()) / 1000));
      var m = Math.floor(s / 60), r = s % 60;
      out.textContent = (m < 10 ? '0' : '') + m + ':' + (r < 10 ? '0' : '') + r;
      if (s <= 0) {
        clearInterval(timer);
        box.hidden = true;
        if (btn) btn.disabled = false;
      }
    };
    var timer = setInterval(tick, 1000);
    tick();
  }
})();
