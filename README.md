# tyr-blog-img

`tyr-blog-img` 是给 `fuwari /gallery/` 提供图源的后端项目（后续目标：Go 爬虫 + D1 + R2）。

当前阶段（MVP 第 1 步）已完成：

- Go 项目骨架
- D1 客户端（Cloudflare API）
- 图库专用表结构（去重、编号、crawler_state）
- 基础数据库方法（source/hash 去重、取下一个编号、写入、统计 counts）

当前阶段（MVP 第 2-3 步）已完成：

- R2 上传客户端（S3 协议）
- `StoreToGallery()` 主入库服务（按正确顺序：先源级去重 -> 再内容级去重 -> 最后分配编号）
- 单实例按 `h/v` 分锁，降低并发下编号冲突风险
- 图片处理器接口（已切到混合模式）
  - `webp` 直通
  - `jpg/png/gif` 通过 `cwebp` 转码成 `webp`

注意：运行时需要系统里可执行 `cwebp`（Zeabur 容器镜像里要安装 `libwebp` 工具）。

## 后续计划（分步骤）

1. 接入 R2 上传（`ri/h/{seq}.webp`, `ri/v/{seq}.webp`）
2. 新增 `StoreToGallery()` 入库流程（按正确顺序：先查重，再转码，最后分配编号）
3. 对接 Pixiv/Twitter/TG 入口（参考 `xin` 的采集逻辑）
4. 提供 `/random.js` `/counts.json` 接口给 `fuwari`

## 启动（当前仅做 schema 初始化与健康检查）

设置 D1 环境变量后运行：

- `D1_ACCOUNT_ID`
- `D1_API_TOKEN`
- `D1_DATABASE_ID`
- `R2_ENDPOINT`
- `R2_BUCKET`
- `R2_ACCESS_KEY_ID`
- `R2_SECRET_ACCESS_KEY`
- `R2_REGION`（可选，默认 `auto`）

命令：

```powershell
go run ./cmd/server
```

未配置 D1 时也可启动，仅提供 `/healthz`。
