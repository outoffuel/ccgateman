(function() {
  const input = document.getElementById('card-input');
  const loading = document.getElementById('loading-indicator');
  const overlay = document.getElementById('result-overlay');
  const resultCard = document.getElementById('result-card');
  const icon = document.getElementById('result-icon');
  const title = document.getElementById('result-title');
  const msg = document.getElementById('result-message');
  const details = document.getElementById('result-details');
  const resSid = document.getElementById('res-sid');
  const resAttr = document.getElementById('res-attr');
  const resTime = document.getElementById('res-time');
  const focusDot = document.getElementById('focus-dot');
  const focusText = document.getElementById('focus-text');

  let locked = false;
  let dismissTimer = null;

  function tick() {
    const n = new Date();
    document.getElementById('clock').textContent =
      String(n.getHours()).padStart(2,'0')+':'+
      String(n.getMinutes()).padStart(2,'0')+':'+
      String(n.getSeconds()).padStart(2,'0');
    const days = ['日','月','火','水','木','金','土'];
    document.getElementById('clock-date').textContent =
      n.getFullYear()+'年'+(n.getMonth()+1)+'月'+n.getDate()+'日 '+days[n.getDay()]+'曜日';
  }
  tick();
  setInterval(tick, 1000);

  function grabFocus() {
    if (overlay.classList.contains('hidden') && !locked) {
      input.focus();
    }
  }

  function setFocusBadge(active) {
    if (active) {
      focusDot.className = 'inline-block w-2 h-2 rounded-full bg-emerald-400 shadow-sm shadow-emerald-200';
      focusText.textContent = '入力待受中';
    } else {
      focusDot.className = 'inline-block w-2 h-2 rounded-full bg-amber-400 shadow-sm shadow-amber-200';
      focusText.textContent = 'タップしてフォーカス';
    }
  }

  document.addEventListener('click', function(e) {
    const tag = e.target.tagName;
    if (tag === 'BUTTON' || tag === 'A' || tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA') return;
    if (e.target.closest('#result-card')) return;
    grabFocus();
  });

  input.addEventListener('focus', function() { setFocusBadge(true); });
  input.addEventListener('blur', function() {
    setFocusBadge(false);
    setTimeout(grabFocus, 200);
  });

  function lock() {
    locked = true;
    input.disabled = true;
    loading.classList.remove('hidden');
    focusDot.className = 'inline-block w-2 h-2 rounded-full bg-blue-400 animate-pulse-ring';
    focusText.textContent = '処理中...';
  }

  function unlock() {
    locked = false;
    input.disabled = false;
    loading.classList.add('hidden');
    grabFocus();
  }

  input.addEventListener('keydown', function(e) {
    if (e.key === 'Enter') {
      e.preventDefault();
      if (locked) return;
      const val = input.value.trim();
      if (!val) return;
      lock();
      processScan(val);
    }
  });

  function processScan(cardID) {
    const fd = new FormData();
    fd.append('card_id', cardID);
    input.value = '';

    fetch('/api/scan', { method:'POST', body:fd })
      .then(function(r) { return r.json(); })
      .then(function(data) { showResult(data); })
      .catch(function() {
        locked = false;
        showResult({ success:false, message:'サーバー通信エラーが発生しました。' });
      });
  }

  function showResult(data) {
    const ok = data.success;

    if (ok) {
      resultCard.className = 'result-enter w-full max-w-sm rounded-3xl border-2 p-8 text-center bg-white border-emerald-200 shadow-2xl shadow-emerald-100/50';
      icon.className = 'w-16 h-16 mx-auto mb-5 rounded-full flex items-center justify-center text-3xl bg-emerald-100 text-emerald-500';
      icon.innerHTML = '<svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><polyline points="20 6 9 17 4 12"/></svg>';
      title.textContent = data.status === '退室' ? '退室完了' : '入室完了';
      title.className = 'text-2xl font-bold text-emerald-700 mb-2';
      msg.textContent = data.message || '打刻が完了しました。';
      msg.className = 'text-sm mb-5 leading-relaxed whitespace-pre-wrap text-stone-500';
      if (data.student_id) {
        details.classList.remove('hidden');
        resSid.textContent = data.student_id;
        resAttr.textContent = data.attr_label || '-';
        resTime.textContent = data.timestamp || '-';
      } else {
        details.classList.add('hidden');
      }
    } else {
      resultCard.className = 'result-enter w-full max-w-sm rounded-3xl border-2 p-8 text-center bg-white border-red-200 shadow-2xl shadow-red-100/50';
      icon.className = 'w-16 h-16 mx-auto mb-5 rounded-full flex items-center justify-center text-3xl bg-red-100 text-red-500';
      icon.innerHTML = '<svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="3" stroke-linecap="round" stroke-linejoin="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>';
      title.textContent = 'エラー';
      title.className = 'text-2xl font-bold text-red-700 mb-2';
      msg.textContent = data.message || '打刻に失敗しました。';
      msg.className = 'text-sm mb-5 leading-relaxed whitespace-pre-wrap text-stone-500';
      details.classList.add('hidden');
    }

    overlay.classList.remove('hidden');
    if (dismissTimer) clearTimeout(dismissTimer);
    dismissTimer = setTimeout(dismissResult, 5000);
  }

  function dismissResult() {
    resultCard.className = resultCard.className.replace('result-enter','result-leave');
    setTimeout(function() {
      overlay.classList.add('hidden');
      unlock();
    }, 300);
  }

  overlay.addEventListener('click', function(e) {
    if (e.target === overlay) {
      if (dismissTimer) clearTimeout(dismissTimer);
      dismissResult();
    }
  });

  window.addEventListener('DOMContentLoaded', function() {
    setTimeout(function() {
      input.disabled = false;
      grabFocus();
    }, 200);
  });
})();
