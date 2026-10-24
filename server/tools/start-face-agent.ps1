param([string]$Server='http://192.168.2.1:8088',[string]$User='admin',[int]$Interval=60)
$ErrorActionPreference='Stop'
$projectRoot=(Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$python=(Get-Command python).Source
$agent=Join-Path $PSScriptRoot 'face_agent.py'
$models=Join-Path $projectRoot '.tools\models'
$logs=Join-Path $projectRoot 'release\smart-agent'
New-Item -ItemType Directory -Force $logs | Out-Null
$pidFile=Join-Path $logs 'agent.pid'
if(Test-Path $pidFile){
 $existingId=[int](Get-Content -LiteralPath $pidFile)
 $existing=Get-CimInstance Win32_Process -Filter "ProcessId=$existingId" -ErrorAction SilentlyContinue
 if($existing -and $existing.CommandLine -like '*face_agent.py*'){Write-Host '智能整理已在运行。';return}
}
$previousPassword=$env:LUMINA_FACE_PASSWORD
try{
 if(-not $env:LUMINA_FACE_PASSWORD){
  $secret=Read-Host "相册账号 $User 的密码" -AsSecureString
  $ptr=[Runtime.InteropServices.Marshal]::SecureStringToBSTR($secret)
  try{$env:LUMINA_FACE_PASSWORD=[Runtime.InteropServices.Marshal]::PtrToStringBSTR($ptr)}finally{[Runtime.InteropServices.Marshal]::ZeroFreeBSTR($ptr)}
 }
 # OpenCV on Windows cannot reliably open Unicode absolute model paths.
 $process=Start-Process -FilePath $python -ArgumentList @('-X','utf8','-u',('"'+$agent+'"'),'--host',$Server,'--user',$User,'--models','.tools/models','--watch',[string]$Interval) -WorkingDirectory $projectRoot -WindowStyle Hidden -PassThru -RedirectStandardOutput (Join-Path $logs 'agent.log') -RedirectStandardError (Join-Path $logs 'agent-error.log')
 Set-Content -LiteralPath $pidFile -Value $process.Id
 Write-Host "智能整理已启动。电脑保持开机时，每 $Interval 秒检查新增照片。"
}finally{$env:LUMINA_FACE_PASSWORD=$previousPassword}
