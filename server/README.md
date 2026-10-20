# LuminaGallery v2.0

极光私有云相册 · 为 QWRT (aarch64) 路由器打造的轻量家庭相册服务。

当前进度：**P0–P5 全部完成，已在 192.168.2.1 实测运行**。

---

## 已实现（P2 · 分块续传与归档）

| 能力 | 说明 |
|---|---|
| 分块上传 | 默认 4MB/块（`-upload-chunk` 可调，256KB–64MB），`WriteAt` 按偏移落盘 |
| 断点续传 | 服务端按位图记录已收分片，`GET /api/upload/{sid}` 可恢复；支持乱序 |
| 完整性校验 | 全文件 SHA-256，落盘字节与源文件逐字节一致（实测 MATCH） |
| 秒传去重 | 同空间内 `owner + sha256 + size` 命中直接返回；会先确认文件仍在盘上 |
| 自动归档 | 按 EXIF `DateTimeOriginal` / 文件名时间落到 `YYYY年/YYYY-MM月/` |
| 重名处理 | 同名自动加 `-1`、`-2` 后缀，不覆盖 |
| 会话回收 | 24 小时未完成的临时会话自动清理 |
| 前端上传器 | 点选 / 拖拽 / 粘贴，3 并发分片，IndexedDB 持久化，刷新后可续传 |
| 速率 HUD | 实时 MB/s、已传/总量、剩余秒数 |

**实测（PC → 路由器，Wi-Fi 6）**

| 指标 | 结果 |
|---|---|
| 4MB 分片吞吐 | **25.7 MB/s**（约为 USB2 写带宽 32MB/s 的 80%） |
| 256KB 分片吞吐 | 15.6 MB/s（请求开销主导，故默认 4MB） |
| 30MB 文件哈希+归档 | **133 ms** |

---

## 已实现（P5 · 工程化）

| 能力 | 说明 |
|---|---|
| 开机自启 | `/etc/init.d/lumina`（procd），等盘挂载最多 30s，崩溃自动重启 |
| 一键部署 | `deploy/deploy.sh`：停服 → 备份旧版 → 换二进制 → 装 procd → 自检 → 探活，任一步失败自动回滚 |
| 自检 | `lumina -doctor` 或 `GET /api/doctor`：根目录/数据目录/缓存可写、ffmpeg、DB 完整性、磁盘、空间目录、最近备份 |
| 热备 | `VACUUM INTO` 一致性快照，每 24h 自动一次，保留最近 7 份，也可 `POST /api/backup` 手动触发 |
| 清理 | `POST /api/prune` 删除"索引里有、磁盘上没了"的条目（Samba 手动删文件后使用） |
| 内存 | 扫描/哈希这类突发负载结束后 `debug.FreeOSMemory()` 把堆还给系统 |

### 压测结果（PC → 路由器，真实 HTTP）

| 项目 | 结果 |
|---|---|
| 扫描 300 个新文件 | **231 文件/秒**（1.2s） |
| 缩略图 640（已缓存） | **1242 req/s**，p50 5.9ms / p95 10.1ms |
| 预览图 1600（首次生成） | 66–101 ms/张 |
| 列表 API（并发 12） | **492 req/s**，p50 22.7ms / p95 45.4ms |
| 原图下载（并发 4） | 668 req/s，p50 4.9ms |
| 稳定性 | 基线 RSS 9.6MB，压测峰值 23.4MB，空闲回落 15.9MB |

### 崩溃自愈实测

强杀进程 → procd 在 5 秒内拉起新进程（PID 16555 → 16646），健康检查立即恢复 `ok`。

---
## 已实现（P4 · 人脸，桌面代理方案）

路由器只有 1.2GHz 四核、可用内存 300MB，跑不动人脸识别，也编译不了 ncnn（缺 aarch64 musl 工具链）。
所以采用**分工方案**：电脑做推理，路由器只做存储、聚类和展示。

| 环节 | 在哪 | 说明 |
|---|---|---|
| 检测 | 电脑 | YuNet（0.22MB），输出人脸框 |
| 特征 | 电脑 | SFace（37MB），128 维特征向量 |
| 存储 | 路由器 | `faces` 表，向量以 float32 存 BLOB |
| 聚类 | 路由器 | 余弦相似度 >= 0.50 贪心聚簇，单张不成组 |
| 呈现 | 网页 | 智能抽屉「人物」，可命名，人脸圆图由 ffmpeg 从预览图裁出 |

**用法**

```bash
pip install opencv-python-headless requests numpy
python tools/face_agent.py --host http://192.168.2.1:8088 \
    --user lwm --password ****** --models ../.tools/models
```

**实测**：42 张照片（含 5 张人脸测试图）→ 2.8 秒处理完，检出 5 张人脸，正确聚成 **2 个人物**（3 张 + 2 张），人脸裁剪图 200x252 干净可用。

> 路由器全程不参与推理；代理跑完一次即可，新增照片后再跑只处理增量。

---

## 已实现（P4 · 智能）

| 能力 | 说明 |
|---|---|
| 相似照片归组 | 纯 Go dHash（9×8 灰度，稀疏采样求均值），汉明距离 ≤10 归为一组 |
| 事件聚合 | 按拍摄时间间隔 >8h 或 GPS 跳变 >1° 切分，标题带地点：`杭州 · 2024 年 5 月 10 日` |
| 地点聚类 | 离线城市质心表（110+ 座），haversine 最近匹配，>180km 记「未知地点」 |
| 在线编辑 | 非破坏性：亮度/对比度/饱和度/色温/暗角/旋转 → **另存新副本**，原图不动 |
| 智能抽屉 | 顶栏 ✦ → 相似照片 / 事件 / 地点三个视图 |

**实测**：35 张照片算出 8 组相似（重复上传的同一张图被正确归组）；2 张带 GPS 的照片分别识别为杭州、苏州，事件标题自动带上城市。

> **环境约束**：这台 ffmpeg 4.3.5 **没有 `eq` 滤镜**，编辑改用 `lutrgb`（亮度/对比度）+ `hue`（饱和度）+ `colorbalance`（色温）+ `vignette`（暗角）组合实现。

---

## 已实现（P3 · 家庭功能）

| 能力 | 说明 |
|---|---|
| 登录鉴权 | 每个空间独立密码；PBKDF2-SHA256 (120k 轮)，HttpOnly Cookie，30 天会话 |
| 首次初始化 | 首次打开引导为 lwm / cr 各设一个密码 |
| 修改密码 | 右上角用户名 → 修改密码 |
| 回收站 | 软删除到 `.lumina_trash/`，30 天自动清理，可单条恢复/彻底删除 |
| 多选批量 | 长按或点「多选」进入，支持收藏 / 推送 / 打包 / 回收站 |
| ZIP 打包 | 流式 `zip.Store`（不压缩已压缩媒体），边读边发，不落临时文件 |
| 推送到家庭共享 | `os.Link` 硬链接，**不占额外空间**，跨设备自动退化为复制 |
| 分享链接 | 随机 token、可设有效期、免登录只读、过期即失效 |
| 幻灯片 | 全屏、交叉淡化 + Ken Burns、Wake Lock 防息屏 |

**实测**：未登录 `/api/*` → 401；错误密码 → 401；回收站移入/恢复；硬链接推送后出现在家庭空间；ZIP 2 个文件 71KB/4ms；分享链接免 cookie 访问 200。

> 首次部署的初始密码见 `lumina/README` 的部署步骤输出，**请在右上角「修改密码」后使用**。

---

## 已实现（P1）

| 能力 | 说明 |
|---|---|
| 单静态二进制 | Go 交叉编译，8.8 MB，**常驻内存 8.2 MB**，零运行时依赖 |
| SQLite 索引 | 纯 Go 实现（modernc.org/sqlite），WAL + `synchronous=NORMAL` |
| EXIF 解析 | 自研 JPEG/TIFF 解析：机型、镜头、光圈、ISO、焦段、GPS、拍摄时间、方向 |
| 文件名兜底 | 微信 `mmexport<13位>`、`IMG_yyyyMMdd_HHmmss`、`VID_`、`Screenshot_` 等 |
| 缩略图 | ffmpeg 生成 96px / 640px 两档 JPEG，按 `id%100` 分片；冷生成 **47 ms**，视频封面 **120 ms** |
| 时间轴 | 键集分页（`taken_at,id`）、按月分组、等高对齐行 |
| 查看器 | 大图 / 视频播放、EXIF 面板、键盘 ← → Esc、深链 `#/p/{id}` |
| 媒体服务 | HTTP Range（206）、`Accept-Ranges`、ETag/Last-Modified |
| 空间过滤 | lwm / cr / 家庭共享 / 全部 + 照片 / 视频三态 |
| 增量扫描 | 按 `size+mtime` 判重，31 个文件 **80 ms** |

## 目录约定

```
/mnt/sda1/
├── lwm/手机照片与视频备份/YYYY年/YYYY-MM月/
├── cr/手机照片与视频备份/YYYY年/YYYY-MM月/
├── 家庭共享/
├── .lumina/
│   ├── lumina.db            # 索引（WAL）
│   └── bin/lumina           # 可执行文件
└── .lumina_cache/{m,t,p}/XX/{id}.jpg
```

## 构建

```powershell
# 需要 Go 1.23+；仓库内 .tools/go 为便携工具链
$env:GOROOT="$PWD\.tools\go"; $env:PATH="$env:GOROOT\bin;$env:PATH"
$env:GOPATH="$PWD\.tools\gopath"; $env:GOPROXY="https://goproxy.cn,direct"
$env:GOOS="linux"; $env:GOARCH="arm64"; $env:CGO_ENABLED="0"
cd lumina
go build -trimpath -ldflags "-s -w" -o ..\.tools\lumina-arm64 .
```

## 部署

```sh
# 1. 依赖（缩略图/封面必需）
opkg update && opkg install ffmpeg ffprobe

# 2. 二进制
mkdir -p /mnt/sda1/.lumina/bin
# 上传后：
chmod +x /mnt/sda1/.lumina/bin/lumina

# 3. 首次建索引
/mnt/sda1/.lumina/bin/lumina -root /mnt/sda1 -scan

# 4. 前台运行
/mnt/sda1/.lumina/bin/lumina -root /mnt/sda1 -addr :8088
```

打开 `http://192.168.2.1:8088`。

## 命令行参数

| 参数 | 默认 | 说明 |
|---|---|---|
| `-root` | `/mnt/sda1` | 媒体根目录 |
| `-data` | `<root>/.lumina` | 数据库目录 |
| `-cache` | `<root>/.lumina_cache` | 缩略图目录 |
| `-addr` | `:8088` | 监听地址（不占用 LuCI 的 80/443） |
| `-thumb-conc` | `2` | 缩略图并发（4 核 + 62°C 机身，别调太高） |
| `-upload-chunk` | `4194304` | 上传分片字节数（256KB–64MB） |
| `-scan` | — | 仅扫描一次后退出 |
| `-rescan` | — | 启动时全量重扫 |
| `-probe <file>` | — | 打印单个文件的解析结果（诊断） |

## API

```
GET  /api/health
GET  /api/stats
GET  /api/config
GET  /api/buckets?owner=&kind=
GET  /api/timeline?owner=&kind=&before=<taken_at>_<id>&limit=120
GET  /api/asset/{id}
POST /api/scan
POST /api/login | /api/logout | GET /api/me | POST /api/setup | GET /api/setup-state
POST /api/password
POST /api/trash | POST /api/restore | GET /api/trash | DELETE /api/trash/{id}
POST /api/batch/fav | POST /api/batch/push
POST /api/zip
POST /api/share          -> {token,url}
GET  /s/{token}          # 免登录分享
GET  /api/dupes | GET /api/events | GET /api/places | POST /api/edit
POST /api/smart/rebuild | GET /api/smart/stats
GET  /api/faces/pending       # 代理取待处理队列
POST /api/faces/{id}          # 回传人脸框 + 特征
GET  /api/people | POST /api/people/rebuild | POST /api/people/{id}/name
GET  /f/{face_id}.jpg         # 人脸裁剪图（落 .lumina_cache/face/）
GET  /api/doctor              # 自检项（JSON）
POST /api/backup              # 立即热备
POST /api/prune               # 清理已丢失文件的索引
GET  /land.json               # 离线地图轮廓（45KB）
POST /api/upload/init                  # {owner,name,size} -> {sid,chunk,total}
PUT  /api/upload/{sid}/{idx}           # body = 原始分片字节
GET  /api/upload/{sid}                 # 已收分片（续传用）
POST /api/upload/{sid}/complete        # 哈希 + 去重 + 归档 + 入库
DELETE /api/upload/{sid}
GET  /t/{id}/{m|t|p}.jpg
GET  /m/{id}                 # 原图/原视频，支持 Range
```

## 已知边界

- **无 libx264 / libwebp / libheif**：ffmpeg 4.3.5 只能编码 JPEG，所以缩略图是 JPEG，无法在本机转码 H.264 代理。
- **HEIC 静帧无法解码**：iOS 通过网页上传会自动转 JPEG；SMB 直接拷进来的 HEIC 只会记录文件名时间，不生成缩略图。
- **USB 2.0 限速**：当前 32 MB/s，换 USB3.2 线后可到 300 MB/s 以上。
