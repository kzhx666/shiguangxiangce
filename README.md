# 拾光 · 家庭照片云

家里路由器（OpenWrt, arm64）上的自建相册：Go 服务端 + 网页 SPA + Android App。
照片和视频存在路由器挂载的移动硬盘上，手机拍照后自动备份，全家在局域网内浏览。

## 目录结构

```
server/            Go 服务端（单二进制，内嵌网页资源）
  web/             网页前端（原生 JS，无框架）
  deploy/          OpenWrt 部署脚本
android/           Android App（纯 Java，零第三方依赖）
design/            设计相关的一些素材
```

## 功能

- **时间轴**：按月份分组的瀑布流，支持分页懒加载
- **三个隔离分区**：我的照片 / 家庭共享 / 秘密相册
  - 秘密相册需要指纹或账号密码验证
  - 家庭共享用**硬链接**实现，推送不占额外空间，删除互不影响
- **文件夹 / 相册**：按目录聚合，也支持手动建相册
- **自动备份**：App 扫描手机相册，按文件夹选择备份范围，支持直接备份进秘密相册
- **视频**：客户端边下边播（服务器无解码器，缩略图由手机截帧生成）
- **批量操作**：多选删除、移入秘密、推送共享、收藏、打包 ZIP
- **数据库每日备份**：内置定时任务，压缩保留 14 天

## 服务端

```bash
cd server
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags "-s -w" -o lumina-arm64 .
```

首次启动会用 `LUMINA_ADMIN_PW`（环境变量）作为管理员初始密码；
没配置则随机生成并打印在日志里。**部署后第一件事就是改密码。**

## Android App

```powershell
cd android
$env:LUMINA_KS_PASS = "你的keystore口令"
./build.ps1
```

只用 Android SDK 自带的工具链（aapt2 / javac / d8 / zipalign / apksigner），
不需要 Gradle。签名用的 `lumina-release.jks` **不包含在仓库里**，需要自己生成：

```bash
keytool -genkeypair -keystore lumina-release.jks -alias lumina \
  -keyalg RSA -keysize 2048 -validity 10000
```

## 浏览器访问

部署后访问 `http://<路由器IP>:8088`。

## 说明

- 所有数据都在你自己的硬盘上，没有任何云服务依赖
- 服务端为了在低配路由器上跑，刻意限制了内存和并发（见 `main.go` 的 SetMemoryLimit）
- 视频缩略图由手机在播放时截帧上传，因为路由器上的 ffmpeg 没有可用的 H.264/HEVC 解码器
