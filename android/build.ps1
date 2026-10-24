# 拾光 App 构建脚本（零依赖，只用 Android SDK 的命令行工具）
# 用法：在 PowerShell 里执行  .\build.ps1
$ErrorActionPreference = 'Stop'

$proj = $PSScriptRoot
$sdk = $env:ANDROID_HOME
if (-not $sdk) { $sdk = $env:ANDROID_SDK_ROOT }
if (-not $sdk) { $sdk = Join-Path $env:LOCALAPPDATA 'Android\Sdk' }
$bt   = Join-Path $sdk "build-tools\36.0.0"
$jar  = Join-Path $sdk "platforms\android-36\android.jar"
$localJar=Join-Path $proj "..\.tools\android-platform.jar"
if(Test-Path $localJar){$jar=(Resolve-Path $localJar).Path}
$build = Join-Path $proj "build"
$ks = if ($env:LUMINA_KEYSTORE) { $env:LUMINA_KEYSTORE } else { Join-Path $proj "lumina-release.jks" }
if (-not $env:LUMINA_KS_PASS) { throw "请设置 LUMINA_KS_PASS（签名密钥口令）" }
if (-not $env:LUMINA_KEY_PASS) { $env:LUMINA_KEY_PASS = $env:LUMINA_KS_PASS }

foreach ($p in @($bt, $jar)) { if (-not (Test-Path $p)) { throw "缺少 SDK 组件: $p" } }

# 清理
foreach ($d in @("classes", "dex", "gen")) {
    $full = Join-Path $build $d
    if (Test-Path $full) { Remove-Item $full -Recurse -Force }
    New-Item -ItemType Directory -Force -Path $full | Out-Null
}
New-Item -ItemType Directory -Force -Path $build | Out-Null

# 1) 资源
$resZip = Join-Path $build "res.zip"
if (Test-Path $resZip) { Remove-Item $resZip -Force }
if (Test-Path (Join-Path $proj "res")) {
    Push-Location $proj
    & (Join-Path $bt "aapt2.exe") compile --dir res -o $resZip
    Pop-Location
    if ($LASTEXITCODE -ne 0) { throw "aapt2 compile 失败" }
}

# 2) 链接成基础 APK
$baseApk = Join-Path $build "base.apk"
Push-Location $proj
$linkArgs = @("link", "-o", $baseApk, "-I", $jar, "--manifest", "AndroidManifest.xml",
              "--min-sdk-version", "24", "--target-sdk-version", "34", "--auto-add-overlay")
if (Test-Path $resZip) { $linkArgs += @("-R", $resZip) }
& (Join-Path $bt "aapt2.exe") @linkArgs
Pop-Location
if ($LASTEXITCODE -ne 0) { throw "aapt2 link 失败" }

# 3) 编译 Java
$srcs = Get-ChildItem (Join-Path $proj "src") -Recurse -Filter *.java | ForEach-Object { $_.FullName }
& javac -encoding UTF-8 --release 8 -nowarn -Xlint:none -classpath $jar -d (Join-Path $build "classes") @srcs 2>&1 |
    ForEach-Object { Write-Host $_ }
if ($LASTEXITCODE -ne 0) { throw "javac 失败" }

# 4) dex
# 类太多会超过命令行长度限制：先打个 jar，再让 d8 处理这个 jar
$classesJar = Join-Path $build "classes.jar"
if (Test-Path $classesJar) { Remove-Item $classesJar -Force }
Push-Location (Join-Path $build "classes")
& jar cf $classesJar .
Pop-Location
& (Join-Path $bt "d8.bat") --lib $jar --min-api 24 --output (Join-Path $build "dex") $classesJar
if ($LASTEXITCODE -ne 0) { throw "d8 失败" }
Write-Host "d8 OK"

# 5) 打包
$unsigned = Join-Path $build "app-unsigned.apk"
Copy-Item $baseApk $unsigned -Force
Add-Type -AssemblyName System.IO.Compression.FileSystem
$zip = [IO.Compression.ZipFile]::Open($unsigned, 'Update')
$existing = $zip.GetEntry("classes.dex")
if ($existing) { $existing.Delete() }
[IO.Compression.ZipFileExtensions]::CreateEntryFromFile($zip,
    (Join-Path $build "dex\classes.dex"), "classes.dex") | Out-Null
$zip.Dispose()
Write-Host "classes.dex 已打包"

# 6) 对齐 + 签名
$aligned = Join-Path $build "app-aligned.apk"
& (Join-Path $bt "zipalign.exe") -f 4 $unsigned $aligned
if ($LASTEXITCODE -ne 0) { throw "zipalign 失败" }

$final = Join-Path $build "lumina.apk"
if (-not (Test-Path $ks)) { throw "缺少签名文件: $ks" }
& (Join-Path $bt "apksigner.bat") sign --ks $ks --ks-pass env:LUMINA_KS_PASS --key-pass env:LUMINA_KEY_PASS `
    --out $final $aligned
if ($LASTEXITCODE -ne 0) { throw "apksigner 失败" }

$mb = [int]((Get-Item $final).Length / 1024)
Write-Host "APK OK: $final  $mb KB"
