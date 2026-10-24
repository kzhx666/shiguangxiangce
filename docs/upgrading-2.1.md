# 2.1 升级说明

服务端版本 2.1.0；安卓版本 2.0（versionCode 2）。保留旧账号空间映射与原有媒体目录。新增成员使用独立目录。

## 功能入口

- **上传任务**：按账号和服务器保存任务；支持暂停、重试、取消，以及应用进程重启后的续传。手机读取权限失效时需要重新选择文件。浏览器需要允许 IndexedDB 保存文件。
- **搜索筛选**：文件名、文件夹、相机、镜头、已命名人物、城市附近匹配；组合日期、照片／视频、收藏和当前空间，支持保存常用筛选。城市匹配基于离线坐标范围，不包含 OCR 或语义搜图。
- **家庭成员**：管理员可增加成员、修改空间名称、设置上传配额和家庭共享访问权限、重设成员密码。配额包含存量文件与上传预留空间，不是磁盘层硬配额。普通成员不能添加账号。
- **人物纠错**：支持命名、同空间人物合并、将误分人脸拆为新人物。增量归类保留已有人工结果。
- **相似照片对比**：显示照片与文件大小，用户确认后移入回收站；每组至少保留一张，不自动删除。

## 构建

服务端需要 Go 1.23+。在 `server/` 执行 `go test ./...`。面向路由器构建时设置 `GOOS=linux`、`GOARCH=arm64`、`CGO_ENABLED=0` 后运行 `go build -trimpath -ldflags "-s -w" -o lumina-arm64 .`。

Android 使用 `android/build.ps1`，需要 JDK、Android SDK build-tools 36.0.0 和 android-36 平台。SDK 从 `ANDROID_HOME` / `ANDROID_SDK_ROOT` 或用户默认 SDK 目录读取。签名文件由 `LUMINA_KEYSTORE` 指定，默认 `android/lumina-release.jks`；设置 `LUMINA_KS_PASS`，私钥口令不同时另设 `LUMINA_KEY_PASS`。密钥和口令不应提交。

首次建库保留仓库原有 `LUMINA_ADMIN_PW` 管理员密码初始化方式；已有账号密码不会因升级改变。

## 电脑端持续识别

安装 `opencv-python-headless`、`requests`、`numpy`，将 YuNet 和 SFace ONNX 模型放入仓库根目录 `.tools/models/`。运行 `server/tools/start-face-agent.ps1 -Server http://路由器地址:8088 -User 管理员账号`，根据提示输入相册密码。默认每 60 秒检查一次，使用本机计算，不把照片发送到外部服务。

停止时运行 `server/tools/stop-face-agent.ps1`。电脑重启后需要重新启动脚本；不会自动安装系统启动任务。日志位于忽略提交的 `release/smart-agent/`。也可以直接运行 `face_agent.py --watch 60`，密码可通过临时环境变量 `LUMINA_FACE_PASSWORD` 提供。

## 部署与回退

升级前保存旧程序、启动配置和停止服务后的数据库快照。`server/deploy/upgrade-2.1.sh` 适用于既有 `/mnt/sda1/.lumina` 部署，先备份再替换程序，健康检查失败时恢复旧程序。其他路径请先调整脚本。

不要直接以旧数据库覆盖升级后的数据库，否则会丢失新产生的记录。安卓覆盖安装须使用与旧版一致的签名；不要通过卸载来尝试无损降级。

## 验证

- Go 集成测试覆盖成员隔离和配额、组合搜索、缺块拒绝完成及会话续传、人物纠错后重建保留。
- 路由器独立测试：三块图片乱序上传，缺块拒绝完成，恢复后 SHA-256 一致。
- Android 真机测试：上传一块后暂停，在新应用进程中复用原会话恢复，文件 SHA-256 一致。
- JavaScript / Python 语法检查、Android 签名构建、既有相册接口回归通过。

临时测试账号、个人照片、数据库、调试截图、设备日志、令牌及签名密钥不属于源码交付。
