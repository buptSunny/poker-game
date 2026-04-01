# Texas Hold'em Online 在线德州扑克

多人实时在线德州扑克游戏，Go 后端 + 原生 HTML/JS 前端。

## 快速启动

```bash
cd backend
go run .
```

浏览器打开 http://localhost:8080

## 游戏说明

1. 输入昵称，创建或加入房间（输入4位房间号）
2. 房间内所有玩家点击「准备」后自动开局
3. 每人初始筹码 1000，小盲/大盲自动扣除
4. 支持操作：弃牌(Fold)、过牌(Check)、跟注(Call)、加注(Raise)、全押(All-in)
5. 30秒超时自动弃牌
6. 结算后筹码归零玩家自动淘汰，点「准备」继续下一局

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.22 + net/http |
| 实时通信 | gorilla/websocket |
| 前端 | 原生 HTML/CSS/JS（无构建工具）|

## 项目结构

```
game/
├── backend/
│   ├── main.go           # 入口
│   ├── server/
│   │   ├── http.go       # REST API（房间列表/创建）
│   │   └── ws.go         # WebSocket Hub
│   └── game/
│       ├── deck.go       # 洗牌/发牌
│       ├── hand_eval.go  # 七选五最优牌型判断
│       ├── game.go       # 游戏状态机
│       └── room.go       # 房间管理
└── frontend/
    ├── index.html        # 大厅
    ├── game.html         # 游戏桌面
    ├── style.css
    └── js/
        ├── ws.js         # WebSocket 客户端
        ├── lobby.js      # 大厅逻辑
        └── game.js       # 游戏交互
```

## 多人测试

同一台电脑开多个浏览器 Tab，输入不同昵称加入同一房间即可模拟多人对局。
