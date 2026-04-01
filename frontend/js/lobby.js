const API = '';

// ===== Auth state =====
let authMode = 'login'; // 'login' | 'register'
let currentUser = null; // { token, userId, username, chips }

function loadSession() {
  try {
    const raw = localStorage.getItem('pokerSession');
    if (raw) currentUser = JSON.parse(raw);
  } catch (_) {}
}

function saveSession(user) {
  currentUser = user;
  localStorage.setItem('pokerSession', JSON.stringify(user));
}

function clearSession() {
  currentUser = null;
  localStorage.removeItem('pokerSession');
  localStorage.removeItem('lastRoomId');
}

// ===== Auth UI =====
function toggleAuthMode() {
  authMode = authMode === 'login' ? 'register' : 'login';
  const isLogin = authMode === 'login';
  document.getElementById('authTitle').textContent   = isLogin ? '欢迎回来' : '创建账号';
  document.getElementById('authSub').textContent     = isLogin ? '登录你的账号继续游戏' : '注册一个新账号';
  document.getElementById('authBtn').textContent     = isLogin ? '登 录' : '注 册';
  document.getElementById('authToggle').textContent  = isLogin ? '没有账号？点此注册' : '已有账号？点此登录';
  document.getElementById('authError').textContent   = '';
}

async function doAuth() {
  const username = document.getElementById('authUsername').value.trim();
  const password = document.getElementById('authPassword').value;
  const errEl    = document.getElementById('authError');
  errEl.textContent = '';

  if (!username || !password) { errEl.textContent = '请填写用户名和密码'; return; }

  const endpoint = authMode === 'login' ? '/auth/login' : '/auth/register';
  try {
    const res  = await fetch(API + endpoint, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ username, password })
    });
    const data = await res.json();
    if (data.error) { errEl.textContent = data.error; return; }
    saveSession(data);
    showLobby();
  } catch (e) {
    errEl.textContent = '网络错误: ' + e.message;
  }
}

// Enter key support — script runs after DOM is ready (bottom of body)
['authUsername', 'authPassword'].forEach(id => {
  document.getElementById(id).addEventListener('keydown', e => {
    if (e.key === 'Enter') doAuth();
  });
});

async function doAnonymous() {
  const errEl = document.getElementById('authError');
  errEl.textContent = '';
  try {
    const res = await fetch(API + '/auth/anonymous', { method: 'POST' });
    const data = await res.json();
    if (data.error) { errEl.textContent = data.error; return; }
    saveSession(data);
    showLobby();
  } catch (e) {
    errEl.textContent = '网络错误: ' + e.message;
  }
}

function logout() {
  clearSession();
  document.getElementById('lobbyPage').style.display  = 'none';
  document.getElementById('authOverlay').style.display = 'flex';
  document.getElementById('authUsername').value = '';
  document.getElementById('authPassword').value = '';
}

function showLobby() {
  document.getElementById('authOverlay').style.display = 'none';
  document.getElementById('lobbyPage').style.display   = 'block';
  document.getElementById('ubarName').textContent    = currentUser.username;
  document.getElementById('ubarChips').textContent   = currentUser.chips;
  document.getElementById('ubarAvatar').textContent  = currentUser.username[0].toUpperCase();
  // Refresh chips + stats from server
  fetch(`${API}/auth/me?token=${encodeURIComponent(currentUser.token)}`)
    .then(r => r.json())
    .then(data => {
      if (data.chips !== undefined) {
        currentUser.chips = data.chips;
        localStorage.setItem('pokerSession', JSON.stringify(currentUser));
        document.getElementById('ubarChips').textContent = data.chips;
      }
      if (data.handsPlayed !== undefined) {
        document.getElementById('ubarStats').textContent =
          `${data.handsPlayed} 局 · 胜 ${data.handsWon} 局`;
      }
    })
    .catch(() => {});
  loadRooms();
  checkLeaderboardVisibility();
  loadMyHands();
  checkRejoin();

  // Auto-fill room ID from share link (?join=XXXX)
  const joinParam = new URLSearchParams(location.search).get('join');
  if (joinParam) {
    history.replaceState({}, '', location.pathname);
    enterRoom(joinParam);
  }
}

// ===== Rooms =====
async function loadRooms() {
  if (!currentUser) return;
  try {
    const res   = await fetch(`${API}/rooms`);
    const rooms = await res.json();
    const tbody = document.getElementById('roomTbody');
    if (!rooms || rooms.length === 0) {
      tbody.innerHTML = '<tr><td colspan="5" style="color:#666;text-align:center">暂无房间，快来创建一个！</td></tr>';
      return;
    }
    tbody.innerHTML = rooms.map(r => `
      <tr>
        <td><strong>#${r.id}</strong></td>
        <td>${r.players}/${r.maxPlayers}</td>
        <td>${r.smallBlind}/${r.smallBlind * 2}</td>
        <td>${(r.startingChips||1000).toLocaleString()}</td>
        <td><span class="badge ${r.phase === 'waiting' ? 'badge-wait' : 'badge-play'}">${r.phase === 'waiting' ? '等待中' : '游戏中'}</span></td>
        <td><button class="btn btn-blue btn-sm" onclick="enterRoom('${r.id}')">加入</button></td>
      </tr>
    `).join('');
  } catch (e) {
    console.error(e);
  }
}

async function createRoom() {
  const maxPlayers = parseInt(document.getElementById('maxPlayers').value);
  const smallBlind = parseInt(document.getElementById('smallBlind').value) || 10;
  const startingChips = parseInt(document.getElementById('startingChips').value) || 1000;
  try {
    const res  = await fetch(`${API}/rooms`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ maxPlayers, smallBlind, startingChips })
    });
    const data = await res.json();
    if (data.error) { alert(data.error); return; }
    enterRoom(data.roomId);
  } catch (e) {
    alert('创建房间失败: ' + e.message);
  }
}

function joinRoom() {
  const roomId = document.getElementById('joinRoomId').value.trim();
  if (!roomId) { alert('请输入房间号'); return; }
  enterRoom(roomId);
}

function enterRoom(roomId) {
  sessionStorage.setItem('roomId', roomId);
  localStorage.setItem('lastRoomId', roomId); // for rejoin
  location.href = 'game.html';
}

// ===== Rejoin =====
function checkRejoin() {
  const lastRoom = localStorage.getItem('lastRoomId');
  if (!lastRoom) return;
  fetch(`${API}/rooms`)
    .then(r => r.json())
    .then(rooms => {
      const found = (rooms || []).find(r => r.id === lastRoom);
      // Only show if room is mid-game AND this player is still in it
      if (found && found.phase !== 'waiting' &&
          (found.playerIds || []).includes(currentUser.userId)) {
        const banner = document.getElementById('rejoinBanner');
        banner.classList.add('show');
        banner.dataset.roomId = lastRoom;
      } else {
        localStorage.removeItem('lastRoomId');
      }
    })
    .catch(() => {});
}

function rejoinGame() {
  const roomId = document.getElementById('rejoinBanner').dataset.roomId;
  if (roomId) enterRoom(roomId);
}

function dismissRejoin() {
  localStorage.removeItem('lastRoomId');
  document.getElementById('rejoinBanner').classList.remove('show');
}

// ===== Leaderboard =====
async function checkLeaderboardVisibility() {
  if (!currentUser) return;
  try {
    const [settingRes, adminRes] = await Promise.all([
      fetch(`${API}/settings/leaderboard`),
      fetch(`${API}/auth/admin?token=${encodeURIComponent(currentUser.token)}`)
    ]);
    const setting = await settingRes.json();
    const admin = await adminRes.json();
    const section = document.getElementById('leaderboardSection');
    const isAdmin = admin.isAdmin;

    if (setting.visible) {
      section.style.display = '';
      loadLeaderboard();
    } else {
      section.style.display = 'none';
    }

    // Admin toggle button
    if (isAdmin) {
      let toggleBtn = document.getElementById('lbToggleBtn');
      if (!toggleBtn) {
        const header = section.querySelector('.sec-header');
        toggleBtn = document.createElement('button');
        toggleBtn.id = 'lbToggleBtn';
        toggleBtn.className = 'btn btn-ghost btn-sm';
        header.appendChild(toggleBtn);
      }
      toggleBtn.textContent = setting.visible ? '隐藏排行榜' : '显示排行榜';
      toggleBtn.onclick = () => toggleLeaderboard(!setting.visible);
      // If hidden, still show section for admin with a message
      if (!setting.visible) {
        section.style.display = '';
        document.getElementById('lbTbody').innerHTML =
          '<tr><td colspan="6" style="color:#666;text-align:center;padding:18px">排行榜已关闭</td></tr>';
      }
    }
  } catch (e) {
    console.error('leaderboard setting error', e);
    loadLeaderboard(); // fallback: show it
  }
}

async function toggleLeaderboard(visible) {
  try {
    await fetch(`${API}/settings/leaderboard?token=${encodeURIComponent(currentUser.token)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ visible })
    });
    checkLeaderboardVisibility();
  } catch (e) {
    console.error('toggle leaderboard error', e);
  }
}

async function loadLeaderboard() {
  if (!currentUser) return;
  try {
    const res  = await fetch(`${API}/leaderboard?limit=10`);
    const rows = await res.json();
    const tbody = document.getElementById('lbTbody');
    if (!rows || rows.length === 0) {
      tbody.innerHTML = '<tr><td colspan="6" style="color:#666;text-align:center">暂无数据</td></tr>';
      return;
    }
    tbody.innerHTML = rows.map((r, i) => {
      const isMe = r.username === currentUser.username;
      const medal = i === 0 ? '🥇' : i === 1 ? '🥈' : i === 2 ? '🥉' : i + 1;
      return `<tr style="${isMe ? 'color:var(--gold);font-weight:600' : ''}">
        <td>${medal}</td>
        <td>${esc(r.username)}${isMe ? ' 👈' : ''}</td>
        <td>${r.chips.toLocaleString()}</td>
        <td>${r.handsWon} / ${r.handsPlayed}</td>
        <td>${r.totalWon.toLocaleString()}</td>
        <td>${r.biggestPot.toLocaleString()}</td>
      </tr>`;
    }).join('');
  } catch (e) {
    console.error('leaderboard error', e);
  }
}

function esc(s) {
  return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;');
}

// ===== My Hand History =====
async function loadMyHands() {
  if (!currentUser) return;
  const el = document.getElementById('myHandsList');
  try {
    const res   = await fetch(`${API}/auth/hands?token=${encodeURIComponent(currentUser.token)}`);
    const hands = await res.json();
    if (!hands || hands.length === 0) {
      el.innerHTML = '<span style="color:#666">暂无历史对局</span>';
      return;
    }
    el.innerHTML = hands.slice(0, 5).map(h => renderHand(h, currentUser.userId)).join('');
  } catch (e) {
    el.innerHTML = '<span style="color:#e74c3c">加载失败</span>';
  }
}

function cardStr(c) {
  if (!c) return '';
  const suit = (c.Suit || c.suit || '').toLowerCase();
  const rank = c.Rank || c.rank;
  const suits = { s:'♠', h:'♥', d:'♦', c:'♣' };
  const ranks = { 14:'A', 13:'K', 12:'Q', 11:'J' };
  const isRed = suit === 'h' || suit === 'd';
  const symbol = suits[suit] || suit;
  const label = ranks[rank] || String(rank);
  return `<span class="mini-card ${isRed ? 'red' : 'black'}">${symbol}${label}</span>`;
}

function renderHand(h, myUserId) {
  const me  = (h.players || []).find(p => p.playerId === myUserId);
  const net = me?.net ?? ((me?.won ?? 0) - (me?.bet ?? 0));
  const netClass = net > 0 ? 'win' : net < 0 ? 'loss' : 'even';
  const netStr   = net > 0 ? `+${net}` : net < 0 ? `${net}` : '±0';
  const date = new Date(h.endedAt * 1000).toLocaleString('zh-CN', { month:'numeric', day:'numeric', hour:'2-digit', minute:'2-digit' });

  let community = '';
  try { community = JSON.parse(h.community).map(cardStr).join(''); } catch {}

  const players = (h.players || []).map(p => {
    const cls = [
      'player-tag',
      p.playerId === myUserId ? 'is-me' : '',
      p.isWinner ? 'is-winner' : '',
      p.handRank === '弃牌' ? 'is-folded' : '',
    ].filter(Boolean).join(' ');
    return `<span class="${cls}">${esc(p.name)} ${p.handRank}${p.isWinner ? ' 🏆' : ''}</span>`;
  }).join('');

  return `<div class="hand-item">
    <div class="hand-header">
      <span class="hand-room">房间 #${esc(h.roomId)} · ${date}</span>
      <div class="hand-result">
        <span class="hand-pot">底池 ${(h.pot||0).toLocaleString()}</span>
        <span class="hand-net ${netClass}">${netStr}</span>
      </div>
    </div>
    ${community ? `<div class="hand-community">${community}</div>` : ''}
    <div class="hand-players">${players}</div>
  </div>`;
}

// ===== Init =====
loadSession();
if (currentUser) {
  showLobby();
}
setInterval(loadRooms, 5000);
