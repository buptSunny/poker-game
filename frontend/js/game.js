// ===== Init =====
const roomId = sessionStorage.getItem('roomId');

// Load auth session
const _session = (() => {
  try { return JSON.parse(localStorage.getItem('pokerSession') || 'null'); } catch(_) { return null; }
})();

if (!roomId || !_session) { location.href = 'index.html'; throw new Error('redirect'); }

const myId   = _session.userId;
const myName = _session.username;
const myToken = _session.token;

document.getElementById('hdrRoom').textContent = '#' + roomId;

let gameState  = null;
let myHand     = [];
let isMyTurn   = false;
let turnOptions = [];
let callAmount = 0;
let minRaise   = 0;
let timerInterval = null;
let isSpectator = false;

const sock = new GameSocket(roomId, myId, myName, myToken, handleMessage);
sock.connect();

// ===== Turn sound =====
function playTurnSound() {
  try {
    const ctx = new (window.AudioContext || window.webkitAudioContext)();
    [523, 659, 784].forEach((freq, i) => {
      const osc = ctx.createOscillator();
      const gain = ctx.createGain();
      osc.connect(gain); gain.connect(ctx.destination);
      osc.frequency.value = freq;
      osc.type = 'sine';
      gain.gain.setValueAtTime(0.3, ctx.currentTime + i * 0.12);
      gain.gain.exponentialRampToValueAtTime(0.001, ctx.currentTime + i * 0.12 + 0.2);
      osc.start(ctx.currentTime + i * 0.12);
      osc.stop(ctx.currentTime + i * 0.12 + 0.2);
    });
  } catch (_) {}
}

// ===== Message handler =====
function handleMessage(msg) {
  switch (msg.type) {
    case 'game_state': onGameState(msg.payload); break;
    case 'deal':       onDeal(msg.payload); break;
    case 'your_turn':  onYourTurn(msg.payload); break;
    case 'showdown':   onShowdown(msg.payload); break;
    case 'chat':       onChat(msg.payload); break;
    case 'spectator':  onSpectator(msg.payload); break;
    case 'emoji':      onEmoji(msg.payload); break;
    case 'error':      showStatus('⚠ ' + msg.payload.message, true); break;
  }
}

function onSpectator(payload) {
  isSpectator = payload.spectating;
  if (isSpectator) {
    showStatus('👁 观战模式 — 你正在观看这局游戏');
    document.getElementById('actionButtons').innerHTML = '';
  } else {
    // Promoted from spectator to player, re-render current state
    if (gameState) onGameState(gameState);
  }
}

function onGameState(state) {
  gameState = state;
  document.getElementById('hdrBlinds').textContent = state.smallBlind + '/' + state.bigBlind;
  document.getElementById('hdrPot').textContent    = state.pot;
  document.getElementById('potAmount').textContent = state.pot;

  renderCommunity(state.community || []);
  renderSeats(state);
  checkBetAnimations(state);

  if (isSpectator) {
    if (state.phase === 'waiting') {
      showStatus('👁 正在加入游戏...');
      sock.send('join', {});
    } else {
      showStatus('👁 观战模式 — 你正在观看这局游戏');
    }
    return;
  }

  if (state.phase === 'waiting') {
    isMyTurn = false;
    clearActions();
    showStatus('等待玩家准备...（点击"准备"开始游戏）');
    showReadyButton(state);
  } else if (state.phase === 'showdown') {
    isMyTurn = false;
    clearActions();
    showStatus('结算中...');
  } else {
    // In-game: show whose turn it is
    const cp = (state.players || [])[state.currentIdx];
    if (cp && cp.id !== myId) {
      showStatus(`等待 ${cp.name} 操作...`);
    }
  }
}

function onDeal(payload) {
  myHand = payload.hand || [];
  renderMyHand();
}

function onYourTurn(payload) {
  isMyTurn    = true;
  turnOptions = payload.options || [];
  callAmount  = payload.callAmount || 0;
  minRaise    = payload.minRaise || 0;
  showStatus('你的回合！');
  renderActionButtons();
  startTimer(payload.timeout || 30);
  playTurnSound();
}

function onShowdown(payload) {
  clearTimer();
  isMyTurn = false;
  clearActions();
  myHand = [];
  renderMyHand();
  showShowdown(payload);
}

function onChat(payload) {
  const msgs = document.getElementById('chatMessages');
  const p = document.createElement('p');
  p.innerHTML = `<span class="from">${esc(payload.from)}:</span> ${esc(payload.message)}`;
  msgs.appendChild(p);
  msgs.scrollTop = msgs.scrollHeight;
}

// ===== Rendering =====
const SUIT_SYMBOLS = { s: '♠', h: '♥', d: '♦', c: '♣' };
const SUIT_CLASS   = { s: 'black', h: 'red', d: 'red', c: 'black' };
const RANK_LABEL   = { 14:'A', 13:'K', 12:'Q', 11:'J' };

function cardLabel(rank) {
  return RANK_LABEL[rank] || String(rank);
}

function renderCard(card, small) {
  const div = document.createElement('div');
  div.className = `card card-face ${SUIT_CLASS[card.Suit]}`;
  if (small) { div.style.width='36px'; div.style.height='52px'; div.style.fontSize='.75rem'; }
  div.innerHTML = `<span>${cardLabel(card.Rank)}</span><span>${SUIT_SYMBOLS[card.Suit]}</span>`;
  return div;
}

function renderCardBack(small) {
  const div = document.createElement('div');
  div.className = 'card card-back';
  if (small) { div.style.width='36px'; div.style.height='52px'; }
  div.textContent = '🂠';
  return div;
}

let prevCommunityCount = 0; // track for animation

function renderCommunity(cards) {
  const el = document.getElementById('communityCards');
  const newCount = cards.filter(Boolean).length;

  // Only animate newly revealed cards
  if (newCount > prevCommunityCount) {
    // Keep existing cards, only add new ones
    for (let i = prevCommunityCount; i < 5; i++) {
      const existing = el.children[i];
      if (cards[i] && existing) {
        // Replace placeholder with flip animation
        const container = document.createElement('div');
        container.className = 'card-flip-container';
        container.style.cssText = `width:50px;height:72px;`;

        const inner = document.createElement('div');
        inner.className = 'card-flip-inner';
        inner.style.cssText = 'width:100%;height:100%;';

        const back = document.createElement('div');
        back.className = 'card card-back card-flip-back';
        back.textContent = '🂠';

        const front = renderCard(cards[i]);
        front.classList.add('card-flip-front');

        inner.appendChild(back);
        inner.appendChild(front);
        container.appendChild(inner);

        el.replaceChild(container, existing);

        // Trigger flip after brief delay (stagger per card)
        setTimeout(() => inner.classList.add('flipped'), 80 * (i - prevCommunityCount));
      }
    }
    prevCommunityCount = newCount;
  } else if (newCount === 0) {
    // Fresh hand — reset everything
    prevCommunityCount = 0;
    el.innerHTML = '';
    for (let i = 0; i < 5; i++) {
      const ph = document.createElement('div');
      ph.className = 'card card-back';
      ph.style.opacity = '0.25';
      el.appendChild(ph);
    }
  } else if (newCount === prevCommunityCount) {
    // No change, don't re-render
  }
}

// Seat positions around the oval (as % of container)
const SEAT_POSITIONS = [
  { top: '90%', left: '50%' },  // bottom center (you)
  { top: '75%', left: '18%' },
  { top: '42%', left: '6%'  },
  { top: '12%', left: '22%' },
  { top: '8%',  left: '50%' },
  { top: '12%', left: '78%' },
  { top: '42%', left: '94%' },
  { top: '75%', left: '82%' },
  { top: '58%', left: '50%' }, // extra seat
];

function renderSeats(state) {
  const container = document.getElementById('seats');
  container.innerHTML = '';

  const players = state.players || [];
  const myIdx   = players.findIndex(p => p.id === myId);
  const isOwner = state.ownerId === myId;

  players.forEach((p, i) => {
    // rotate so "you" is always at bottom
    let posIdx = i;
    if (myIdx >= 0) {
      posIdx = (i - myIdx + players.length) % players.length;
    }
    const pos = SEAT_POSITIONS[posIdx] || SEAT_POSITIONS[0];

    const seat = document.createElement('div');
    seat.className = 'seat'
      + (i === state.currentIdx && state.phase !== 'waiting' && state.phase !== 'showdown' ? ' active' : '')
      + (p.folded ? ' folded' : '')
      + (p.disconnected ? ' disconnected' : '')
      + (p.id === myId ? ' you' : '');
    seat.style.top  = pos.top;
    seat.style.left = pos.left;

    const dealerChip = p.isDealer ? `<span class="dealer-chip">D</span>` : '';

    let betText = '';
    if (p.bet > 0) betText = `下注: ${p.bet}`;
    if (p.allIn)   betText = '全押';

    const ownerTag = state.ownerId === p.id ? ' <span style="color:#f0c040;font-size:.6rem">★房主</span>' : '';
    const dcTag = p.disconnected ? ' <span style="color:#ef4444;font-size:.6rem">断线</span>' : '';
    const kickBtn = (isOwner && p.id !== myId && state.phase === 'waiting')
      ? `<button onclick="kickPlayer('${p.id}')" style="margin-top:4px;padding:2px 6px;font-size:.6rem;background:#dc2626;color:#fff;border:none;border-radius:4px;cursor:pointer">踢出</button>`
      : '';

    const info = document.createElement('div');
    info.className = 'seat-info';
    info.innerHTML = `
      <div class="seat-name">${esc(p.name)}${ownerTag}${dcTag}${dealerChip}${p.isBot ? ' <span style="color:#7f5af0;font-size:.65rem">BOT</span>' : ''}${p.allIn ? ' <span style="color:#e67e22;font-size:.7rem">ALL IN</span>' : ''}</div>
      <div class="seat-chips">♦ ${p.chips}</div>
      ${betText ? `<div class="seat-bet">${betText}</div>` : ''}
      ${p.isReady && state.phase === 'waiting' ? '<div style="color:#2ecc71;font-size:.7rem">已准备</div>' : ''}
      ${kickBtn}
    `;

    const cards = document.createElement('div');
    cards.className = 'seat-cards';

    if (p.id === myId) {
      // my cards shown in action bar, show backs here too if in game
    } else if (p.hand && p.hand.length > 0) {
      // showdown reveal
      p.hand.forEach((c, ci) => {
        const card = renderCard(c, true);
        card.classList.add('deal-anim');
        card.style.animationDelay = `${ci * 0.1}s`;
        cards.appendChild(card);
      });
    } else if (p.cardCount > 0 && !p.folded) {
      for (let k = 0; k < p.cardCount; k++) {
        const card = renderCardBack(true);
        card.classList.add('deal-anim');
        card.style.animationDelay = `${k * 0.1}s`;
        cards.appendChild(card);
      }
    }

    seat.appendChild(cards);
    seat.appendChild(info);
    container.appendChild(seat);
  });
}

function renderMyHand() {
  const el = document.getElementById('myHand');
  el.innerHTML = '';
  myHand.forEach((c, i) => {
    const card = renderCard(c);
    card.classList.add('deal-anim');
    card.style.animationDelay = `${i * 0.15}s`;
    el.appendChild(card);
  });
}

// ===== Actions =====
function showReadyButton(state) {
  const el = document.getElementById('actionButtons');
  el.innerHTML = '';

  const me = (state.players || []).find(p => p.id === myId);
  const amReady = me && me.isReady;
  const readyBtn = document.createElement('button');
  readyBtn.className = amReady ? 'btn btn-call' : 'btn btn-ready';
  readyBtn.textContent = amReady ? '取消准备' : '准 备';
  readyBtn.onclick = () => { sock.send('ready', {}); };
  el.appendChild(readyBtn);

  // Rebuy button
  const rebuyBtn = document.createElement('button');
  rebuyBtn.className = 'btn btn-call';
  rebuyBtn.textContent = '补充筹码';
  rebuyBtn.onclick = () => {
    if (confirm('补充筹码到初始金额？')) {
      sock.send('rebuy', {});
    }
  };
  el.appendChild(rebuyBtn);

  // Add Bot button
  const botBtn = document.createElement('button');
  botBtn.className = 'btn btn-call';
  botBtn.textContent = '+ 添加 Bot';
  botBtn.style.cssText = 'background:#7f5af0';
  botBtn.onclick = addBot;
  el.appendChild(botBtn);
}

async function addBot() {
  try {
    const res = await fetch(`/rooms/${roomId}/bots`, { method: 'POST' });
    const data = await res.json();
    if (data.error) { showStatus('添加Bot失败: ' + data.error); }
  } catch (e) {
    showStatus('添加Bot失败: ' + e.message);
  }
}

function kickPlayer(playerId) {
  if (confirm('确认踢出该玩家？')) {
    sock.send('kick', { playerId });
  }
}

function clearActions() {
  document.getElementById('actionButtons').innerHTML = '';
  document.getElementById('timerWrap').hidden = true;
}

function renderActionButtons() {
  const el = document.getElementById('actionButtons');
  el.innerHTML = '';

  const make = (label, cls, fn) => {
    const b = document.createElement('button');
    b.className = `btn ${cls}`;
    b.textContent = label;
    b.onclick = fn;
    el.appendChild(b);
    return b;
  };

  if (turnOptions.includes('fold'))  make('弃牌 Fold',  'btn-fold',  () => doAction('fold'));
  if (turnOptions.includes('check')) make('过牌 Check', 'btn-check', () => doAction('check'));
  if (turnOptions.includes('call'))  make(`跟注 Call ${callAmount}`, 'btn-call', () => doAction('call'));
  if (turnOptions.includes('allin')) make('全押 All-in', 'btn-allin', () => doAction('allin'));
  // "bet" = first bet (no prior bet), "raise" = re-raise; both use same UI
  const hasBetOrRaise = turnOptions.includes('raise') || turnOptions.includes('bet');
  const betAction = turnOptions.includes('bet') ? 'bet' : 'raise';
  const betLabel  = turnOptions.includes('bet') ? '下注 Bet' : '加注 Raise';
  if (hasBetOrRaise) {
    const wrap = document.createElement('div');
    wrap.className = 'raise-input';
    const inp = document.createElement('input');
    inp.type = 'number';
    inp.value = minRaise;
    inp.min = minRaise;
    inp.placeholder = '金额';
    const btn = document.createElement('button');
    btn.className = 'btn btn-raise';
    btn.textContent = betLabel;
    btn.onclick = () => doAction(betAction, parseInt(inp.value) || minRaise);
    wrap.appendChild(inp);
    wrap.appendChild(btn);
    el.appendChild(wrap);
  }
}

function doAction(action, amount) {
  if (!isMyTurn) return;
  sock.send('action', { action, amount: amount || 0 });
  isMyTurn = false;
  clearTimer();
  clearActions();
  showStatus('等待其他玩家...');
}

// ===== Showdown =====
function showShowdown(payload) {
  const overlay = document.getElementById('showdownOverlay');
  // Trigger reflow then add active for CSS transition
  overlay.classList.add('active');

  // community
  const comm = document.getElementById('communityResult');
  comm.innerHTML = '';
  (payload.community || []).forEach(c => comm.appendChild(renderCard(c)));

  // pot breakdown (side pots explanation)
  const potsEl = document.getElementById('potBreakdown');
  if (potsEl) potsEl.remove();
  const pots = payload.pots || [];
  if (pots.length > 0) {
    const breakdown = document.createElement('div');
    breakdown.id = 'potBreakdown';
    breakdown.style.cssText = 'margin-bottom:12px;text-align:left;font-size:.85rem;background:rgba(0,0,0,0.3);padding:10px 14px;border-radius:8px;';
    let html = '';
    if (pots.length > 1) {
      html += '<div style="font-weight:bold;margin-bottom:6px;text-align:center;color:#f0c040">边池分配说明</div>';
    }
    pots.forEach(pot => {
      html += `<div style="margin-bottom:6px;padding:4px 0;${pots.length > 1 ? 'border-bottom:1px solid rgba(255,255,255,0.1)' : ''}">`;
      if (pots.length > 1) {
        const eligibleStr = pot.eligible.join('、');
        html += `<span style="color:#2ecc71;font-weight:bold">${esc(pot.label)}</span>`;
        html += ` <span style="color:#f0c040">${pot.amount}</span> 筹码`;
        html += ` — 参与者: ${esc(eligibleStr)}<br>`;
      }
      html += `<span style="color:#aaa">→</span> ${esc(pot.reason)}`;
      html += `</div>`;
    });
    breakdown.innerHTML = html;
    comm.parentNode.insertBefore(breakdown, comm.nextSibling);
  }

  // results with staggered animation
  const el = document.getElementById('showdownResults');
  el.innerHTML = '';
  const sorted = (payload.results || []).sort((a,b) => b.won - a.won);
  sorted.forEach((r, idx) => {
    const row = document.createElement('div');
    row.className = 'result-row' + (r.isWinner ? ' winner' : '');
    row.style.animationDelay = `${idx * 0.1}s`;

    const cards = document.createElement('div');
    cards.style.cssText = 'display:flex;gap:3px;';
    (r.hand || []).forEach(c => {
      const cc = renderCard(c, true);
      cards.appendChild(cc);
    });

    row.innerHTML = `
      <div class="rname">${r.isWinner ? '🏆 ' : ''}${esc(r.name)}</div>
    `;
    row.appendChild(cards);
    row.innerHTML += `<div class="rhand">${esc(r.handDesc || r.handRank || '')}</div>
      <div class="rwon">${r.won > 0 ? '+' + r.won : ''}</div>`;
    el.appendChild(row);
  });

  // Animate chips from pot to winner seats
  const winnerIds = sorted.filter(r => r.isWinner).map(r => r.playerId || '');
  if (winnerIds.length > 0 && gameState) {
    setTimeout(() => animateChipsToWinners(winnerIds), 400);
  }
}

function closeShowdown() {
  document.getElementById('showdownOverlay').classList.remove('active');
  myHand = [];
  renderMyHand();
  prevCommunityCount = 0;
}

// ===== Timer =====
function startTimer(seconds) {
  clearTimer();
  const wrap = document.getElementById('timerWrap');
  const fill = document.getElementById('timerFill');
  wrap.hidden = false;
  fill.style.transition = 'none';
  fill.style.width = '100%';
  setTimeout(() => {
    fill.style.transition = `width ${seconds}s linear`;
    fill.style.width = '0%';
  }, 50);

  let left = seconds;
  timerInterval = setInterval(() => {
    left--;
    if (left <= 0) clearTimer();
  }, 1000);
}

function clearTimer() {
  if (timerInterval) { clearInterval(timerInterval); timerInterval = null; }
  document.getElementById('timerWrap').hidden = true;
}

// ===== Chat =====
function sendChat() {
  const inp = document.getElementById('chatInput');
  const msg = inp.value.trim();
  if (!msg) return;
  sock.send('chat', { message: msg });
  inp.value = '';
}

// ===== Share room =====
function shareRoom() {
  const url = `${location.origin}${location.pathname.replace('game.html', '')}index.html?join=${roomId}`;
  // Try modern clipboard API first, fall back to execCommand
  if (navigator.clipboard && window.isSecureContext) {
    navigator.clipboard.writeText(url).then(() => {
      showStatus('房间链接已复制到剪贴板！');
      setTimeout(() => showStatus(''), 3000);
    }).catch(() => copyFallback(url));
  } else {
    copyFallback(url);
  }
}

function copyFallback(text) {
  const ta = document.createElement('textarea');
  ta.value = text;
  ta.style.cssText = 'position:fixed;opacity:0';
  document.body.appendChild(ta);
  ta.select();
  try {
    document.execCommand('copy');
    showStatus('房间链接已复制到剪贴板！');
    setTimeout(() => showStatus(''), 3000);
  } catch (_) {
    prompt('复制这个链接分享给朋友:', text);
  }
  document.body.removeChild(ta);
}

// ===== Misc =====
function showStatus(msg) {
  document.getElementById('statusMsg').textContent = msg;
}

function leaveGame() {
  localStorage.removeItem('lastRoomId'); // clean leave — no rejoin needed
  sock.close();
  location.href = 'index.html';
}

function esc(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

// ===== Chip Animations =====
let prevBets = {}; // track previous bets for animation

function animateChipToPot(seatEl) {
  const potEl = document.getElementById('potAmount');
  if (!seatEl || !potEl) return;

  const seatRect = seatEl.getBoundingClientRect();
  const potRect  = potEl.getBoundingClientRect();
  const dx = potRect.left - seatRect.left;
  const dy = potRect.top - seatRect.top;

  const chip = document.createElement('div');
  chip.className = 'chip-token';
  chip.textContent = '$';
  chip.style.left = seatRect.left + seatRect.width/2 - 11 + 'px';
  chip.style.top  = seatRect.top + seatRect.height/2 - 11 + 'px';
  chip.style.position = 'fixed';
  chip.style.setProperty('--dx', dx + 'px');
  chip.style.setProperty('--dy', dy + 'px');

  document.getElementById('emojiEffects').appendChild(chip);
  chip.addEventListener('animationend', () => chip.remove());
}

function animateChipsToWinners(winnerIds) {
  const potEl = document.getElementById('potAmount');
  if (!potEl) return;
  const potRect = potEl.getBoundingClientRect();
  const seats = document.querySelectorAll('.seat');

  seats.forEach(seat => {
    const nameEl = seat.querySelector('.seat-name');
    if (!nameEl) return;
    // Check if this seat's player is a winner via the seat element
    const seatRect = seat.getBoundingClientRect();

    // Match winner by checking seat position
    const players = gameState ? gameState.players || [] : [];
    const myIdx = players.findIndex(p => p.id === myId);

    players.forEach((p, i) => {
      if (!winnerIds.includes(p.id)) return;
      let posIdx = i;
      if (myIdx >= 0) posIdx = (i - myIdx + players.length) % players.length;
      const pos = SEAT_POSITIONS[posIdx];
      if (!pos) return;

      // Find the seat at this position
      if (seat.style.top !== pos.top || seat.style.left !== pos.left) return;

      const dx = potRect.left - seatRect.left;
      const dy = potRect.top - seatRect.top;

      for (let c = 0; c < 5; c++) {
        const chip = document.createElement('div');
        chip.className = 'chip-win';
        chip.style.left = seatRect.left + seatRect.width/2 - 11 + 'px';
        chip.style.top  = seatRect.top - 5 + 'px';
        chip.style.position = 'fixed';
        chip.style.setProperty('--dx', dx + 'px');
        chip.style.setProperty('--dy', dy + 'px');
        chip.style.animationDelay = `${c * 0.08}s`;

        document.getElementById('emojiEffects').appendChild(chip);
        chip.addEventListener('animationend', () => chip.remove());
      }
    });
  });
}

// Check for new bets in renderSeats and animate
function checkBetAnimations(state) {
  const players = state.players || [];
  players.forEach(p => {
    const prevBet = prevBets[p.id] || 0;
    if (p.bet > prevBet && p.bet > 0) {
      // Find the seat element for this player
      setTimeout(() => {
        const seats = document.querySelectorAll('.seat');
        const myIdx = players.findIndex(pp => pp.id === myId);
        const pIdx = players.indexOf(p);
        let posIdx = pIdx;
        if (myIdx >= 0) posIdx = (pIdx - myIdx + players.length) % players.length;
        const pos = SEAT_POSITIONS[posIdx];
        if (!pos) return;

        seats.forEach(seat => {
          if (seat.style.top === pos.top && seat.style.left === pos.left) {
            animateChipToPot(seat);
          }
        });
      }, 50);
    }
    prevBets[p.id] = p.bet;
  });
  // Reset bets when phase is waiting (new hand)
  if (state.phase === 'waiting') {
    prevBets = {};
  }
}

// ===== Emoji System =====
const EMOJIS = [
  { key: 'thumbsup', emoji: '👍', label: '点赞' },
  { key: 'laugh',    emoji: '😂', label: '大笑' },
  { key: 'fire',     emoji: '🔥', label: '火' },
  { key: 'poop',     emoji: '💩', label: '臭' },
  { key: 'tomato',   emoji: '🍅', label: '番茄' },
  { key: 'coins',    emoji: '💰', label: '撒金币' },
  { key: 'party',    emoji: '🎉', label: '庆祝' },
  { key: 'angry',    emoji: '😡', label: '愤怒' },
];

// Build emoji panel on load
(function initEmojiPanel() {
  const panel = document.getElementById('emojiPanel');
  if (!panel) return;
  EMOJIS.forEach(e => {
    const btn = document.createElement('button');
    btn.className = 'emoji-item';
    btn.innerHTML = `<span>${e.emoji}</span><span>${e.label}</span>`;
    btn.onclick = () => {
      sock.send('emoji', { emoji: e.key });
      panel.classList.remove('show');
    };
    panel.appendChild(btn);
  });
})();

function toggleEmojiPanel() {
  document.getElementById('emojiPanel').classList.toggle('show');
}

// Close emoji panel when clicking outside
document.addEventListener('click', (e) => {
  const panel = document.getElementById('emojiPanel');
  if (panel && !e.target.closest('.emoji-trigger') && !e.target.closest('.emoji-panel')) {
    panel.classList.remove('show');
  }
});

function onEmoji(payload) {
  const emojiDef = EMOJIS.find(e => e.key === payload.emoji);
  if (!emojiDef) return;

  const layer = document.getElementById('emojiEffects');

  switch (payload.emoji) {
    case 'tomato':
      playTomatoRain(layer);
      break;
    case 'coins':
      playCoinFountain(layer);
      break;
    case 'party':
      playConfetti(layer);
      break;
    default:
      playEmojiFloat(layer, emojiDef.emoji);
      break;
  }
}

function playEmojiFloat(layer, emoji) {
  const el = document.createElement('div');
  el.className = 'emoji-floating';
  el.textContent = emoji;
  // Random horizontal position in middle third
  el.style.left = (30 + Math.random() * 40) + '%';
  el.style.top  = '60%';
  layer.appendChild(el);
  el.addEventListener('animationend', () => el.remove());
}

function playTomatoRain(layer) {
  for (let i = 0; i < 15; i++) {
    setTimeout(() => {
      const t = document.createElement('div');
      t.className = 'emoji-tomato';
      t.textContent = '🍅';
      t.style.left = (5 + Math.random() * 90) + '%';
      t.style.top  = '-50px';
      t.style.animationDuration = (0.8 + Math.random() * 0.6) + 's';
      layer.appendChild(t);
      t.addEventListener('animationend', () => t.remove());
    }, i * 80);
  }
}

function playCoinFountain(layer) {
  const cx = window.innerWidth / 2;
  const cy = window.innerHeight * 0.5;
  for (let i = 0; i < 25; i++) {
    setTimeout(() => {
      const c = document.createElement('div');
      c.className = 'emoji-coin';
      c.textContent = '🪙';
      c.style.left = cx + 'px';
      c.style.top  = cy + 'px';
      const angle = (Math.random() * Math.PI * 2);
      const dist  = 80 + Math.random() * 200;
      c.style.setProperty('--tx', Math.cos(angle) * dist + 'px');
      c.style.setProperty('--ty', Math.sin(angle) * dist - 100 + 'px');
      c.style.animationDuration = (1 + Math.random() * 0.8) + 's';
      layer.appendChild(c);
      c.addEventListener('animationend', () => c.remove());
    }, i * 40);
  }
}

function playConfetti(layer) {
  const colors = ['#f0c040','#ef4444','#22c55e','#3b82f6','#a855f7','#ec4899','#f97316'];
  const cx = window.innerWidth / 2;
  const cy = window.innerHeight * 0.35;
  for (let i = 0; i < 60; i++) {
    setTimeout(() => {
      const p = document.createElement('div');
      p.className = 'confetti-piece';
      p.style.left = cx + 'px';
      p.style.top  = cy + 'px';
      p.style.background = colors[Math.floor(Math.random() * colors.length)];
      p.style.width  = (5 + Math.random() * 6) + 'px';
      p.style.height = (5 + Math.random() * 6) + 'px';
      const angle = Math.random() * Math.PI * 2;
      const dist  = 100 + Math.random() * 300;
      p.style.setProperty('--tx', Math.cos(angle) * dist + 'px');
      p.style.setProperty('--ty', Math.sin(angle) * dist + 'px');
      p.style.setProperty('--rot', (360 + Math.random() * 720) + 'deg');
      p.style.animationDuration = (1.2 + Math.random() * 1) + 's';
      layer.appendChild(p);
      p.addEventListener('animationend', () => p.remove());
    }, i * 15);
  }
}

