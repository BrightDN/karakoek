(function () {
  var body = document.body;
  var unlock = document.getElementById('unlock');
  var player = null, ready = false, pending = [];
  var currentId = null;        // request id of the cued song, or null
  var STATE = { '-1': 'cued', 0: 'ended', 1: 'playing', 2: 'paused', 3: 'playing', 5: 'cued' };

  var ws = connectWS('screen', {
    GetVideo: function (p) { run(function () { cue(p.requestId, p.videoId); }); },
    PlayVideo: function () { run(play); },
    PauseVideo: function () { run(function () { player.pauseVideo(); }); },
    RestartVideo: function () { run(function () { player.seekTo(0, true); play(); }); },
    FinishVideo: function () { run(clear); },
    UndoVideo: function () { run(clear); }
  });

  function run(fn) { if (ready) fn(); else pending.push(fn); }

  function report(state) {
    if (currentId) ws.send('player_state', { state: state, requestId: currentId });
  }

  function cue(id, videoId) {
    currentId = id;
    body.classList.remove('idle');
    unlock.hidden = true;
    player.cueVideoById(videoId); // cue only: never loadVideoById, never autoplay
  }

  function play() {
    player.playVideo();
    // Browsers may block scripted playback until the page has had a click.
    setTimeout(function () {
      if (currentId && player.getPlayerState() !== 1 && player.getPlayerState() !== 3) unlock.hidden = false;
    }, 1500);
  }

  function clear() {
    currentId = null;
    unlock.hidden = true;
    try { player.stopVideo(); } catch (e) {}
    body.classList.add('idle');
  }

  unlock.addEventListener('click', function () { unlock.hidden = true; if (currentId) player.playVideo(); });

  window.onYouTubeIframeApiReady = function () {
    player = new YT.Player('player', {
      width: '100%', height: '100%',
      playerVars: { autoplay: 0, controls: 0, disablekb: 1, fs: 0, modestbranding: 1, rel: 0, iv_load_policy: 3, playsinline: 1 },
      events: {
        onReady: function () { ready = true; pending.splice(0).forEach(function (f) { f(); }); },
        onStateChange: function (e) {
          var s = STATE[e.data];
          if (!s) return;
          if (s === 'ended') { var id = currentId; report('ended'); if (id) { currentId = null; body.classList.add('idle'); } return; }
          if (s === 'playing') unlock.hidden = true;
          report(s);
        }
      }
    });
  };

  var tag = document.createElement('script');
  tag.src = 'https://www.youtube.com/iframe_api';
  document.head.appendChild(tag);
})();
