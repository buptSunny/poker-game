// WebSocket wrapper with auto-reconnect
class GameSocket {
  constructor(roomId, playerId, playerName, token, onMessage) {
    this.roomId = roomId;
    this.playerId = playerId;
    this.playerName = playerName;
    this.token = token;
    this.onMessage = onMessage;
    this.ws = null;
    this._closing = false;
    this._reconnectDelay = 1000;
    this._maxReconnectDelay = 30000;
    this._reconnectTimer = null;
  }

  connect() {
    const proto = location.protocol === 'https:' ? 'wss' : 'ws';
    const url = `${proto}://${location.host}/ws?room=${encodeURIComponent(this.roomId)}&player=${encodeURIComponent(this.playerId)}&name=${encodeURIComponent(this.playerName)}&token=${encodeURIComponent(this.token)}`;
    this.ws = new WebSocket(url);

    this.ws.onopen = () => {
      this._reconnectDelay = 1000;
    };

    this.ws.onmessage = (e) => {
      try {
        const msg = JSON.parse(e.data);
        if (msg.type === 'auth_error') {
          // Token expired or invalid — clear session and send back to login
          this._closing = true;
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
        return;
      }
      this._scheduleReconnect();
    };

    this.ws.onerror = (e) => {
      console.error('ws error', e);
    };
  }

  _scheduleReconnect() {
    if (this._closing || this._reconnectTimer) return;
    console.log(`ws: reconnecting in ${this._reconnectDelay}ms...`);
    this._reconnectTimer = setTimeout(() => {
      this._reconnectTimer = null;
      if (this._closing) return;
      this._reconnectDelay = Math.min(this._reconnectDelay * 2, this._maxReconnectDelay);
      this.connect();
    }, this._reconnectDelay);
  }

  send(type, payload) {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify({ type, payload }));
    }
  }

  close() {
    this._closing = true;
    clearTimeout(this._reconnectTimer);
    if (this.ws) this.ws.close();
  }
}
