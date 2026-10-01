#requires -Version 5.1
<#
ElBot 本地 Windows 容器部署 / 后台控制入口。

默认使用仓库已有的 deploy/docker-compose.yml，在 Docker Desktop（Linux containers）
中运行单实例 ElBot。状态查询复用项目内置的：
    elbot doctor
    /live /ready /healthz
    /tasks /metrics /diagnostics

备份 / 恢复验证 / 升级 / 回滚直接调用 deploy/ 下已有的 Bash 脚本，因此这些动作需要
PATH 中有 Git for Windows 的 bash.exe。

常用：
    .\deploy\windows\elbot.ps1 init
    .\deploy\windows\elbot.ps1 start
    .\deploy\windows\elbot.ps1 status
    .\deploy\windows\elbot.ps1 health
    .\deploy\windows\elbot.ps1 doctor --no-model
    .\deploy\windows\elbot.ps1 logs -Follow -Tail 200
    .\deploy\windows\elbot.ps1 install-service

运行 `.\deploy\windows\elbot.ps1 help` 查看完整动作。
#>

[CmdletBinding(PositionalBinding = $false)]
param(
    [Parameter(Position = 0)]
    [ValidateSet(
        'help',
        'init',
        'build',
        'start',
        'up',
        'recreate',
        'stop',
        'down',
        'restart',
        'status',
        'ps',
        'health',
        'tasks',
        'metrics',
        'diagnostics',
        'doctor',
        'logs',
        'shell',
        'exec',
        'backup',
        'restore-verify',
        'rollback',
        'upgrade',
        'install-service',
        'uninstall-service',
        'version',
        'compose'
    )]
    [string]$Action = 'status',

    [Parameter(Position = 1, ValueFromRemainingArguments = $true)]
    [string[]]$Rest = @(),

    [switch]$Follow,
    [int]$Tail = 200,
    [switch]$NoBuild,
    [switch]$Json
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$script:ExitCode = 0
$script:RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$script:DeployDir = Join-Path $script:RepoRoot 'deploy'
$script:ComposeFile = Join-Path $script:DeployDir 'docker-compose.yml'
$script:EnvFile = Join-Path $script:DeployDir '.env'
$script:EnvExample = Join-Path $script:DeployDir '.env.example'
$script:DataDir = Join-Path $script:DeployDir 'data'
$script:VersionFile = Join-Path $script:DeployDir 'VERSION'
$script:TaskName = 'ElBot-Docker'
$script:HealthBase = if ($env:ELBOT_HEALTH_URL) { $env:ELBOT_HEALTH_URL.TrimEnd('/') } else { 'http://127.0.0.1:32171' }
$script:Version = 'dev'

function Write-Info {
    param([string]$Message)
    Write-Host "==> $Message" -ForegroundColor Green
}

function Write-Step {
    param([string]$Message)
    Write-Host "    $Message"
}

function Write-Warn {
    param([string]$Message)
    Write-Warning $Message
}

function Throw-Error {
    param([string]$Message)
    throw $Message
}

function Get-Version {
    if (Test-Path $script:VersionFile) {
        return (Get-Content -Raw -LiteralPath $script:VersionFile).Trim()
    }
    return 'dev'
}

function Get-DotEnv {
    $map = @{}
    if (-not (Test-Path -LiteralPath $script:EnvFile)) {
        return $map
    }
    foreach ($line in Get-Content -LiteralPath $script:EnvFile) {
        $trimmed = $line.Trim()
        if ($trimmed -eq '' -or $trimmed.StartsWith('#')) {
            continue
        }
        $index = $trimmed.IndexOf('=')
        if ($index -le 0) {
            continue
        }
        $key = $trimmed.Substring(0, $index).Trim()
        $value = $trimmed.Substring($index + 1).Trim()
        if ($value.Length -ge 2) {
            $first = $value.Substring(0, 1)
            $last = $value.Substring($value.Length - 1, 1)
            if (($first -eq '"' -and $last -eq '"') -or ($first -eq "'" -and $last -eq "'")) {
                $value = $value.Substring(1, $value.Length - 2)
            }
        }
        $map[$key] = $value
    }
    return $map
}

function Get-OpsToken {
    if ($env:ELBOT_OPS_TOKEN) {
        return $env:ELBOT_OPS_TOKEN.Trim()
    }
    $dotenv = Get-DotEnv
    if ($dotenv.ContainsKey('ELBOT_OPS_TOKEN')) {
        return ([string]$dotenv['ELBOT_OPS_TOKEN']).Trim()
    }
    return ''
}

function Test-ScheduledTaskExists {
    if (-not (Get-Command Get-ScheduledTask -ErrorAction SilentlyContinue)) {
        return $false
    }
    return [bool](Get-ScheduledTask -TaskName $script:TaskName -ErrorAction SilentlyContinue)
}

function Get-DockerCompose {
    $docker = Get-Command docker -ErrorAction SilentlyContinue
    if ($docker) {
        & $docker.Source compose version *> $null
        if ($LASTEXITCODE -eq 0) {
            return @($docker.Source, 'compose')
        }
    }
    $legacy = Get-Command docker-compose -ErrorAction SilentlyContinue
    if ($legacy) {
        return @($legacy.Source)
    }
    Throw-Error '未找到 docker compose / docker-compose：请安装并启动 Docker Desktop，且切换到 Linux containers。'
}

function Assert-DockerRunning {
    $docker = Get-Command docker -ErrorAction SilentlyContinue
    if (-not $docker) {
        Throw-Error '未找到 docker.exe：请安装 Docker Desktop，并在设置中使用 WSL2 后端 + Linux containers。'
    }
    & $docker.Source info *> $null
    if ($LASTEXITCODE -ne 0) {
        Throw-Error 'Docker 守护进程不可用：请先启动 Docker Desktop，等待托盘状态变为 Running。'
    }
}

function Set-LocalComposeEnv {
    $version = Get-Version
    if (-not $env:ELBOT_VERSION) {
        $env:ELBOT_VERSION = $version
    }
    if (-not $env:ELBOT_IMAGE) {
        $env:ELBOT_IMAGE = "elbot:$version"
    }
}

function Invoke-Compose {
    param(
        [string[]]$ComposeArgs
    )
    Set-LocalComposeEnv
    $compose = Get-DockerCompose
    $exe = $compose[0]
    $cliArgs = @()
    if ($compose.Length -gt 1) {
        $cliArgs += $compose[1..($compose.Length - 1)]
    }
    $cliArgs += @('--env-file', $script:EnvFile, '-f', $script:ComposeFile)
    $cliArgs += $ComposeArgs
    & $exe @cliArgs
    if ($LASTEXITCODE -ne 0) {
        Throw-Error "docker compose 命令失败（exit=$LASTEXITCODE）。如首次运行，请先执行 init 并填写 .env。"
    }
}

function New-ElbotDataDirs {
    foreach ($path in @(
        $script:DataDir,
        (Join-Path $script:DataDir 'config'),
        (Join-Path $script:DataDir 'run'),
        (Join-Path $script:DataDir 'logs'),
        (Join-Path $script:DataDir 'cache'),
        (Join-Path $script:DeployDir 'backups')
    )) {
        New-Item -ItemType Directory -Force -Path $path | Out-Null
    }
}

function Assert-EnvFile {
    if (-not (Test-Path -LiteralPath $script:EnvFile)) {
        Throw-Error "未找到 $script:EnvFile。请先运行 `.\deploy\windows\elbot.ps1 init`，然后编辑 .env 填写 API Key / token。"
    }
}

function Test-PortListening {
    param(
        [string]$Address = '127.0.0.1',
        [int]$Port = 32171
    )
    try {
        $client = New-Object System.Net.Sockets.TcpClient
        $async = $client.BeginConnect($Address, $Port, $null, $null)
        $ok = $async.AsyncWaitHandle.WaitOne(500)
        if ($ok) {
            $client.EndConnect($async)
        }
        $client.Close()
        return $ok
    } catch {
        return $false
    }
}

function ConvertFrom-HealthResponseStream {
    param($Response)
    if ($null -eq $Response) {
        return $null
    }
    try {
        $stream = $Response.GetResponseStream()
        if ($null -eq $stream) {
            return $null
        }
        # Windows PowerShell 5.1 may leave the error body stream at EOF; rewind
        # it before reading so /ready 503 JSON is not lost.
        if ($stream.CanSeek) {
            $stream.Position = 0
        }
        $reader = New-Object System.IO.StreamReader($stream)
        try {
            $text = $reader.ReadToEnd()
        } finally {
            $reader.Dispose()
            $stream.Dispose()
        }
        if (-not $text) {
            return $null
        }
        return $text | ConvertFrom-Json
    } catch {
        return $null
    }
}

function Get-Health {
    param([string]$Path)
    $token = Get-OpsToken
    $headers = @{}
    if ($token) {
        $headers['X-Elbot-Ops-Token'] = $token
        $headers['Authorization'] = "Bearer $token"
    }
    try {
        return Invoke-RestMethod -Method Get -Uri "$script:HealthBase$Path" -Headers $headers -TimeoutSec 5
    } catch {
        # /ready 在 not_ready 时会返回 503 + JSON；尽量把响应体解析出来，
        # 这样 status 不会把“进程活着但未就绪”误报成“接口不可达”。
        $response = $_.Exception.Response
        if ($response) {
            $parsed = ConvertFrom-HealthResponseStream $response
            if ($null -ne $parsed) {
                return $parsed
            }
        }
        return $null
    }
}

function Format-HealthValue {
    param($Value)
    if ($null -eq $Value) {
        return 'unreachable'
    }
    if ($Value -is [string]) {
        return $Value
    }
    if ($Value.PSObject.Properties.Name -contains 'status') {
        $status = [string]$Value.status
        if ($Value.PSObject.Properties.Name -contains 'version' -and $Value.version) {
            $status += " version=$($Value.version)"
        }
        if ($Value.PSObject.Properties.Name -contains 'uptime_seconds') {
            $status += " uptime=$($Value.uptime_seconds)s"
        }
        return $status
    }
    return ($Value | ConvertTo-Json -Depth 4 -Compress)
}

function Get-ContainerInfo {
    try {
        $raw = & docker inspect --format '{{.State.Status}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}|{{.Config.Image}}' elbot 2>$null
        if ($LASTEXITCODE -ne 0 -or -not $raw) {
            return $null
        }
        $line = @($raw)[0].Trim()
        $parts = $line -split '\|', 3
        $state = if ($parts.Length -gt 0) { $parts[0] } else { '' }
        $health = if ($parts.Length -gt 1) { $parts[1] } else { '' }
        $image = if ($parts.Length -gt 2) { $parts[2] } else { '' }
        return [pscustomobject]@{
            State = $state
            Health = $health
            Image = $image
        }
    } catch {
        return $null
    }
}

function Show-Status {
    $dockerOK = $null -ne (Get-Command docker -ErrorAction SilentlyContinue)
    $composeOK = $false
    if ($dockerOK) {
        try {
            $null = Get-DockerCompose
            $composeOK = $true
        } catch {
            $composeOK = $false
        }
    }
    $container = Get-ContainerInfo
    $live = Get-Health '/live'
    $ready = Get-Health '/ready'
    $healthz = Get-Health '/healthz'

    $summary = [ordered]@{
        version = $script:Version
        compose_file = $script:ComposeFile
        env_file = $script:EnvFile
        data_dir = $script:DataDir
        docker_available = $dockerOK
        compose_available = $composeOK
        task_registered = Test-ScheduledTaskExists
        container = if ($container) { "$($container.State)/$($container.Health) image=$($container.Image)" } else { 'not_found' }
        live = Format-HealthValue $live
        ready = Format-HealthValue $ready
        healthz = Format-HealthValue $healthz
        ops_token_configured = [bool](Get-OpsToken)
        checked_at = (Get-Date).ToString('o')
    }

    if ($Json) {
        $summary | ConvertTo-Json -Depth 8
        if (-not $dockerOK -or $null -eq $live) {
            $script:ExitCode = 1
        }
        return
    }

    Write-Host ''
    Write-Host 'ElBot Windows 本地容器状态' -ForegroundColor Cyan
    Write-Host "  版本        : $($summary.version)"
    Write-Host "  Compose     : $($summary.compose_file)"
    Write-Host "  .env        : $($summary.env_file)"
    Write-Host "  数据目录    : $($summary.data_dir)"
    Write-Host "  Docker      : $(if ($dockerOK) { 'available' } else { 'missing' })"
    Write-Host "  Compose     : $(if ($composeOK) { 'available' } else { 'missing' })"
    Write-Host "  容器        : $($summary.container)"
    Write-Host "  计划任务    : $(if ($summary.task_registered) { $script:TaskName } else { '未注册' })"
    Write-Host "  /live       : $($summary.live)"
    Write-Host "  /ready      : $($summary.ready)"
    Write-Host "  /healthz    : $($summary.healthz)"
    Write-Host "  Ops token   : $(if ($summary.ops_token_configured) { '已配置' } else { '未配置（仅回环可访问）' })"

    Write-Host ''
    Write-Host '常用命令：' -ForegroundColor Cyan
    Write-Host '  .\deploy\windows\elbot.ps1 start'
    Write-Host '  .\deploy\windows\elbot.ps1 health'
    Write-Host '  .\deploy\windows\elbot.ps1 doctor --no-model'
    Write-Host '  .\deploy\windows\elbot.ps1 logs -Follow -Tail 200'
    Write-Host '  .\deploy\windows\elbot.ps1 tasks'
    Write-Host '  .\deploy\windows\elbot.ps1 metrics'
    Write-Host '  .\deploy\windows\elbot.ps1 diagnostics'

    if (-not $dockerOK -or $null -eq $container -or $null -eq $live) {
        $script:ExitCode = 1
    }
}

function Wait-ElbotReady {
    param([int]$TimeoutSeconds = 90)
    Write-Info "等待 ElBot 就绪（最长 ${TimeoutSeconds}s）"
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    while ((Get-Date) -lt $deadline) {
        $ready = Get-Health '/ready'
        if ($ready -and ($ready.ready -eq $true -or $ready.status -eq 'ready')) {
            return $true
        }
        Start-Sleep -Seconds 2
    }
    return $false
}

function Invoke-Init {
    New-ElbotDataDirs
    if (-not (Test-Path -LiteralPath $script:EnvFile)) {
        Copy-Item -LiteralPath $script:EnvExample -Destination $script:EnvFile
        Write-Info "已从 .env.example 生成 $script:EnvFile"
    } else {
        Write-Info "$script:EnvFile 已存在，未覆盖"
    }
    Write-Host ''
    Write-Host '下一步：' -ForegroundColor Cyan
    Write-Host "  1. 编辑 $script:EnvFile，至少填写 LLM API Key、ELBOT_CLI_LOCAL_TOKEN、ELNIS_HOME_TOKEN（如启用）。"
    Write-Host "  2. 运行 .\deploy\windows\elbot.ps1 start 构建并后台启动容器。"
    Write-Host "  3. 运行 .\deploy\windows\elbot.ps1 status 查看容器和 /live /ready /healthz。"
    Write-Host '  4. 首次启动后按 deploy/windows/README.md 检查 data/config/elbot/*.toml。'
}

function Resolve-Bash {
    $bash = Get-Command bash.exe -ErrorAction SilentlyContinue
    if (-not $bash) {
        $bash = Get-Command bash -ErrorAction SilentlyContinue
    }
    if (-not $bash) {
        Throw-Error '该动作复用 deploy/*.sh，需要 Git for Windows 的 bash.exe；请安装 Git 并确保 bash 在 PATH 中，或直接在 Git Bash 中运行 deploy 下的对应脚本。'
    }
    return $bash.Source
}

function Convert-ToBashPath {
    param([string]$Path)
    $value = $Path -replace '\\', '/'
    if ($value -match '^([A-Za-z]):/(.*)$') {
        return '/' + $matches[1].ToLowerInvariant() + '/' + $matches[2]
    }
    return $value
}

function Quote-Bash {
    param([string]$Value)
    return "'" + $Value.Replace("'", "'\''") + "'"
}

function Resolve-BashArgPath {
    param([string]$Value)
    if (Test-Path -LiteralPath $Value) {
        $resolved = (Resolve-Path -LiteralPath $Value).Path
        return Convert-ToBashPath $resolved
    }
    return Convert-ToBashPath $Value
}

function Invoke-BashScript {
    param(
        [string]$ScriptPath,
        [string[]]$ScriptArgs = @()
    )
    $bash = Resolve-Bash
    $deployUnix = Convert-ToBashPath $script:DeployDir
    $scriptUnix = Convert-ToBashPath $ScriptPath
    $command = "cd $(Quote-Bash $deployUnix) && bash $(Quote-Bash $scriptUnix)"
    if ($ScriptArgs.Count -gt 0) {
        $quoted = foreach ($arg in $ScriptArgs) { Quote-Bash (Resolve-BashArgPath $arg) }
        $command = "$command $($quoted -join ' ')"
    }
    Write-Info "调用 Bash 脚本：$ScriptPath"
    & $bash -lc $command
    if ($LASTEXITCODE -ne 0) {
        Throw-Error "$ScriptPath 执行失败（exit=$LASTEXITCODE）"
    }
}

function Show-LogsHint {
    Write-Host '查看日志：.\deploy\windows\elbot.ps1 logs -Follow -Tail 200' -ForegroundColor Yellow
    Write-Host '聚合诊断：.\deploy\windows\elbot.ps1 diagnostics' -ForegroundColor Yellow
}

function Show-JsonValue {
    param($Value)
    $Value | ConvertTo-Json -Depth 12
}

function Invoke-OpsEndpoint {
    param([string]$Path)
    $data = Get-Health $Path
    if ($null -eq $data) {
        Throw-Error "$Path 不可达。确认容器正在运行、32171 端口已映射，且 ELBOT_OPS_TOKEN 与服务端一致。"
    }
    return $data
}

function Install-ScheduledTask {
    $scriptPath = $PSCommandPath
    if (-not $scriptPath -or -not (Test-Path -LiteralPath $scriptPath)) {
        $scriptPath = Join-Path $PSScriptRoot 'elbot.ps1'
    }
    $action = New-ScheduledTaskAction -Execute 'powershell.exe' -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$scriptPath`" start -NoBuild"
    $trigger = New-ScheduledTaskTrigger -AtLogOn
    $settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -StartWhenAvailable -ExecutionTimeLimit ([TimeSpan]::Zero)
    Register-ScheduledTask -TaskName $script:TaskName -Action $action -Trigger $trigger -Settings $settings -Description '登录后自动启动 ElBot Docker 容器（本地 Windows 部署）' -Force | Out-Null
    Write-Info "已注册计划任务 $script:TaskName：登录 Windows 后自动执行 start -NoBuild"
    Write-Host '  查看：Get-ScheduledTask -TaskName ElBot-Docker'
    Write-Host '  移除：.\deploy\windows\elbot.ps1 uninstall-service'
}

function Uninstall-ScheduledTask {
    if (-not (Test-ScheduledTaskExists)) {
        Write-Info "计划任务 $script:TaskName 不存在"
        return
    }
    Unregister-ScheduledTask -TaskName $script:TaskName -Confirm:$false
    Write-Info "已移除计划任务 $script:TaskName"
}

function Show-Help {
    Write-Host @'
ElBot Windows 本地容器部署 / 后台控制

用法：
  .\deploy\windows\elbot.ps1 <动作> [参数...]

部署生命周期：
  init                 创建 deploy/data 和 .env（不覆盖已有 .env）
  build                构建当前源码版本的镜像
  start / up           后台启动容器（默认 --build；可加 -NoBuild）
  recreate             强制重建容器并重新注入 .env
  stop                 停止容器（保留容器和数据）
  down                 停止并移除容器（保留 deploy/data）
  restart              重启容器，不重新注入 .env
  status / ps          查看容器状态 + /live /ready /healthz；-Json 输出 JSON
  version              查看源码版本和容器内版本

运行状态 / 诊断（复用项目内置接口与 elbot doctor）：
  health               查看 /live /ready /healthz
  tasks                查看 /tasks：活跃任务、阶段、排队积压
  metrics              查看 /metrics：任务/资源/模型/限速/生图队列
  diagnostics          查看 /diagnostics：聚合诊断
  doctor [选项]        容器内运行 elbot doctor；可追加 --e2e、--no-model、--json
  logs                 查看容器日志；支持 -Follow -Tail N

运维 / 数据安全（复用 deploy/*.sh，需要 Git for Windows bash）：
  backup [目录]        调用 deploy/backup.sh 做一致性备份
  restore-verify <包>  调用 deploy/restore-verify.sh 做隔离恢复验证
  upgrade              调用 deploy/upgrade.sh
  rollback             调用 deploy/rollback.sh

后台与高级用法：
  install-service      注册 Windows 计划任务：登录后自动 start -NoBuild
  uninstall-service    移除计划任务
  shell                进入容器 bash
  exec <命令...>       在容器内执行命令（docker compose exec -T）
  compose <参数...>    直接透传给 docker compose
  help                 显示本帮助

说明：
  - 使用 Docker Desktop + Linux containers；不要切换到 Windows containers。
  - 状态接口默认 http://127.0.0.1:32171；可用 ELBOT_HEALTH_URL 覆盖。
  - /tasks /metrics /diagnostics 在设置 ELBOT_OPS_TOKEN 后会自动带 token。
  - 本地源码版本以 deploy/VERSION 为准；脚本会设置 ELBOT_VERSION/ELBOT_IMAGE 后再调用 compose。
'@
}

$script:Version = Get-Version
$verb = $Action.ToLowerInvariant()
if ($verb -eq 'up') { $verb = 'start' }
if ($verb -eq 'ps') { $verb = 'status' }

try {
    switch ($verb) {
        'help' {
            Show-Help
        }
        'init' {
            Invoke-Init
        }
        'build' {
            Assert-EnvFile
            Assert-DockerRunning
            New-ElbotDataDirs
            Invoke-Compose (@('build') + @($Rest))
            Write-Info '镜像构建完成'
        }
        'start' {
            Assert-EnvFile
            Assert-DockerRunning
            New-ElbotDataDirs
            $upArgs = @('up', '-d', '--remove-orphans')
            if (-not $NoBuild) {
                $upArgs += '--build'
            }
            Invoke-Compose ($upArgs + @($Rest))
            if (-not (Wait-ElbotReady)) {
                Write-Warn '容器已启动，但 /ready 在 90 秒内未就绪。'
                Show-LogsHint
                $script:ExitCode = 1
            } else {
                Write-Info 'ElBot 已就绪'
                Show-Status
            }
        }
        'recreate' {
            Assert-EnvFile
            Assert-DockerRunning
            New-ElbotDataDirs
            Invoke-Compose (@('up', '-d', '--remove-orphans', '--force-recreate', '--build') + @($Rest))
            if (-not (Wait-ElbotReady)) {
                Write-Warn '容器已重建，但 /ready 在 90 秒内未就绪。'
                Show-LogsHint
                $script:ExitCode = 1
            } else {
                Write-Info 'ElBot 已重建并就绪'
                Show-Status
            }
        }
        'stop' {
            Assert-EnvFile
            Assert-DockerRunning
            Invoke-Compose (@('stop') + @($Rest))
            Write-Info 'ElBot 容器已停止（数据保留在 deploy/data）'
        }
        'down' {
            Assert-EnvFile
            Assert-DockerRunning
            Invoke-Compose (@('down') + @($Rest))
            Write-Info 'ElBot 容器已移除（数据保留在 deploy/data）'
        }
        'restart' {
            Assert-EnvFile
            Assert-DockerRunning
            Invoke-Compose (@('restart') + @($Rest))
            if (-not (Wait-ElbotReady 60)) {
                Write-Warn '容器已重启，但 /ready 在 60 秒内未就绪。'
                Show-LogsHint
                $script:ExitCode = 1
            } else {
                Write-Info 'ElBot 已重启并就绪'
                Show-Status
            }
        }
        'status' {
            Show-Status
        }
        'health' {
            Assert-DockerRunning
            $live = Get-Health '/live'
            $ready = Get-Health '/ready'
            $healthz = Get-Health '/healthz'
            if ($Json) {
                [ordered]@{ live = $live; ready = $ready; healthz = $healthz } | ConvertTo-Json -Depth 8
            } else {
                Write-Host "/live    : $(Format-HealthValue $live)"
                Write-Host "/ready   : $(Format-HealthValue $ready)"
                Write-Host "/healthz : $(Format-HealthValue $healthz)"
            }
            if ($null -eq $live) {
                $script:ExitCode = 1
            }
        }
        'tasks' {
            Show-JsonValue (Invoke-OpsEndpoint '/tasks')
        }
        'metrics' {
            Show-JsonValue (Invoke-OpsEndpoint '/metrics')
        }
        'diagnostics' {
            Show-JsonValue (Invoke-OpsEndpoint '/diagnostics')
        }
        'doctor' {
            Assert-EnvFile
            Assert-DockerRunning
            $doctorArgs = @('exec', '-T', 'elbot', 'elbot', 'doctor')
            if ($Json) {
                $doctorArgs += '--json'
            }
            $doctorArgs += $Rest
            Invoke-Compose $doctorArgs
        }
        'logs' {
            Assert-EnvFile
            Assert-DockerRunning
            $logArgs = @('logs', '--tail', [string]$Tail)
            if ($Follow) {
                $logArgs += '-f'
            }
            $logArgs += 'elbot'
            $logArgs += $Rest
            Invoke-Compose $logArgs
        }
        'shell' {
            Assert-EnvFile
            Assert-DockerRunning
            Invoke-Compose @('exec', 'elbot', 'bash')
        }
        'exec' {
            Assert-EnvFile
            Assert-DockerRunning
            if (@($Rest).Count -eq 0) {
                Throw-Error '用法：.\deploy\windows\elbot.ps1 exec <命令...>'
            }
            Invoke-Compose (@('exec', '-T', 'elbot') + @($Rest))
        }
        'backup' {
            Assert-EnvFile
            Assert-DockerRunning
            $backupScript = Join-Path $script:DeployDir 'backup.sh'
            Invoke-BashScript $backupScript $Rest
        }
        'restore-verify' {
            Assert-DockerRunning
            if (@($Rest).Count -eq 0) {
                Throw-Error '用法：.\deploy\windows\elbot.ps1 restore-verify <deploy\backups\elbot-data-*.tar.gz>'
            }
            $restoreScript = Join-Path $script:DeployDir 'restore-verify.sh'
            Invoke-BashScript $restoreScript $Rest
        }
        'rollback' {
            Assert-EnvFile
            Assert-DockerRunning
            $rollbackScript = Join-Path $script:DeployDir 'rollback.sh'
            Invoke-BashScript $rollbackScript $Rest
        }
        'upgrade' {
            Assert-EnvFile
            Assert-DockerRunning
            $upgradeScript = Join-Path $script:DeployDir 'upgrade.sh'
            Invoke-BashScript $upgradeScript $Rest
        }
        'install-service' {
            Assert-EnvFile
            Install-ScheduledTask
        }
        'uninstall-service' {
            Uninstall-ScheduledTask
        }
        'version' {
            Write-Host "源码版本 : $script:Version"
            $live = Get-Health '/live'
            if ($live -and $live.version) {
                Write-Host "容器版本 : $($live.version)"
            } else {
                Write-Host '容器版本 : 不可用（容器未运行或 32171 未映射）'
            }
            try {
                $compose = Get-DockerCompose
                Write-Host "Compose  : $($compose -join ' ')"
            } catch {
                Write-Host 'Compose  : 不可用（未找到 docker compose）'
            }
        }
        'compose' {
            if (@($Rest).Count -eq 0) {
                Throw-Error '用法：.\deploy\windows\elbot.ps1 compose <参数...>'
            }
            Invoke-Compose $Rest
        }
        default {
            Show-Help
        }
    }
} catch {
    Write-Host "elbot: $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}

exit $script:ExitCode
