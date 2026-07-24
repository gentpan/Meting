# Meting.io

Meting.io 是一个独立部署的 Go 音乐聚合 API。

当前实现提供固定路径风格的接口：

- `GET /healthz`
- `GET /api/v1/providers`
- `GET /api/v1/:provider/search?q=...&page=1&limit=...`
- `GET /api/v1/:provider/songs/:id`
- `GET /api/v1/:provider/songs/:id/stream`
- `GET /api/v1/:provider/songs/:id/cover`
- `GET /api/v1/:provider/songs/:id/lyric`
- `GET /api/v1/:provider/playlists/:id`
- `GET /api/v1/:provider/albums/:id`
- `GET /api/v1/:provider/artists/:id`

已内置的平台：

- `netease`
- `tencent`
- `kugou`

## Run

```bash
cp .env.example .env
go mod tidy
go run ./cmd/metingio
```

默认端口为 `18087`。

## 账号后台 (/admin)

内置一个运营后台,用于扫码登录、管理三家平台凭证、检测登录有效期。

- 访问 `http://<host>:<port>/admin`,用 `MUSICBI_TOKEN` 登录。
- 支持扫码登录:网易云、QQ音乐(QQ / 微信扫码)、酷狗。扫码成功后凭证自动入库,并立即做一次有效性探测。
- 也可手动粘贴 cookie;可随时「重新检测」或「删除」。
- 取流统一走 `music-lib`:酷狗 VIP 直链通过其概念版(appid 3116)签名链路获取;网易/QQ 在原生取流失败时回退到 `music-lib`。
- 凭证存于 SQLite(与 token 同库),provider 运行时动态读取,扫码后立即生效,无需重启。

后台 API(均需 `Authorization: Bearer <MUSICBI_TOKEN>`):

- `GET /api/v1/admin/credentials` — 账号状态列表
- `POST|GET /api/v1/admin/qr/:source` — 创建 / 轮询扫码会话(`source`: `netease|qq|qq_wx|kugou`)
- `POST|DELETE /api/v1/admin/credentials/:provider` — 手动设置 / 删除 cookie
- `POST /api/v1/admin/check/:provider` — 重新探测有效性

## Notes

- `PUBLIC_BASE_URL` 建议在线上设置为 `https://api.pancdn.net`，这样所有标准化 `url / cover / lyric` 字段会统一回到 API 域名。
- `QQ` 搜索优先走当前签名桌面 RPC，必要时再回退 smartbox。
- `KUGOU_COOKIE` 能提升酷狗直链成功率。
