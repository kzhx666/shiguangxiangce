$projectRoot=(Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$pidFile=Join-Path $projectRoot 'release\smart-agent\agent.pid'
if(Test-Path $pidFile){
 $workerId=[int](Get-Content -LiteralPath $pidFile)
 $worker=Get-CimInstance Win32_Process -Filter "ProcessId=$workerId" -ErrorAction SilentlyContinue
 if($worker -and $worker.CommandLine -like '*face_agent.py*'){
  Stop-Process -Id $workerId
  Write-Host '智能整理已停止。'
 }
}
