// WebSocket wrapper
class GameSocket {
  constructor(roomId, playerId, playerName, token, onMessage) {
    this.roomId = roomId;
    this.playerId = playerId;
    this.playerName = playerName;
    this.token = token;
    this.onMessage = onMessage;
    this.ws = null;
    this._closing = false;
  }

  connect() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${proto}://${location.host}/ws?room=${encodeURIComponent(this.roomId)}&player=${encodeURIComponent(this.playerId)}&name=${encodeURIComponent(this.playerName)}&token=${encodeURIComponent(this.token)}`;
    this.ws = new WebSocket(url);

    this.ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.type === 'auth_error') {
          // Token expired or invalid — clear session and send back to login
          localStorage.removeItem('pokerSession');
          alert(msg.payload.message || '登录已过期，请重新登录');
          location.href = 'index.html';
          return;
        }
        this.onMessage(msg);
      } catch (err) {
        console.error('ws parse error', err);
      }
    };

    this.ws.onclose = (e) => {
      if (this._closing) return;
      // Code 4001: server rejected due to auth
      if (e.code === 4001) {
        localStorage.removeItem('pokerSession');
        location.href = 'index.html';
      }
    };

    this.ws.onerror = (e) => {
      console.error('ws error', e);
    };
  }

  send(type, payload) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type, payload }));
    }
  }

  close() {
    this._closing = true;
    if (this.ws) this.ws.close();
  }
}
