#Requires -RunAsAdministrator
<#
.SYNOPSIS
  Instala o pier-agent como Windows Service.

.DESCRIPTION
  Idempotente: se já houver service instalado, para e reinstala.
  - Remove a flag "downloaded from internet" do .exe (Unblock-File)
  - Adiciona o diretório do agente como exclusão do Microsoft Defender
  - Cria o base_path (default C:\rede\clientes) se não existir
  - Instala o service via "agent.exe -service install" e dá start

.EXAMPLE
  # Padrão: agent.exe e config.yaml ao lado deste script
  .\install.ps1

.EXAMPLE
  # Build ARM64
  .\install.ps1 -AgentExe .\agent-arm64.exe

.EXAMPLE
  # Caminho customizado
  .\install.ps1 -AgentExe C:\pier\agent.exe -ConfigPath C:\pier\config.yaml -BasePath D:\rede\clientes
#>
param(
    [string]$AgentExe    = (Join-Path $PSScriptRoot "agent.exe"),
    [string]$ConfigPath  = (Join-Path $PSScriptRoot "config.yaml"),
    [string]$BasePath    = "C:\rede\clientes",
    [string]$ServiceName = "pier-agent"
)

$ErrorActionPreference = "Stop"

function Step($msg) { Write-Host "==> $msg" -ForegroundColor Cyan }
function Ok($msg)   { Write-Host "    $msg" -ForegroundColor Green }

if (-not (Test-Path $AgentExe))   { throw "Agent binary not found: $AgentExe" }
if (-not (Test-Path $ConfigPath)) { throw "Config file not found: $ConfigPath" }

$AgentExe   = (Resolve-Path $AgentExe).Path
$ConfigPath = (Resolve-Path $ConfigPath).Path
$AgentDir   = Split-Path -Parent $AgentExe

Step "Unblocking $AgentExe (clears MOTW/SmartScreen flag)"
Unblock-File -Path $AgentExe
Ok "ok"

Step "Adding Defender exclusion for $AgentDir"
try {
    Add-MpPreference -ExclusionPath $AgentDir -ErrorAction Stop
    Ok "ok"
} catch {
    Write-Warning "Could not add Defender exclusion (Defender disabled or third-party AV?): $($_.Exception.Message)"
}

Step "Ensuring base path exists: $BasePath"
if (-not (Test-Path $BasePath)) {
    New-Item -ItemType Directory -Path $BasePath -Force | Out-Null
    Ok "created"
} else {
    Ok "already exists"
}

$existing = Get-Service -Name $ServiceName -ErrorAction SilentlyContinue
if ($existing) {
    Step "Service '$ServiceName' already exists — stopping and uninstalling first"
    if ($existing.Status -eq "Running") {
        & $AgentExe -service stop -service-name $ServiceName | Out-Host
    }
    & $AgentExe -service uninstall -service-name $ServiceName | Out-Host
    Ok "removed"
}

Step "Installing service '$ServiceName'"
& $AgentExe -service install -service-name $ServiceName -config $ConfigPath | Out-Host
if ($LASTEXITCODE -ne 0) { throw "service install failed (exit $LASTEXITCODE)" }
Ok "installed"

Step "Starting service"
& $AgentExe -service start -service-name $ServiceName | Out-Host
if ($LASTEXITCODE -ne 0) { throw "service start failed (exit $LASTEXITCODE)" }
Ok "started"

Step "Final status"
Get-Service -Name $ServiceName | Format-Table -AutoSize
Write-Host "Logs em: Event Viewer -> Windows Logs -> Application (Source: $ServiceName)" -ForegroundColor DarkGray
