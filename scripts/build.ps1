param([switch]$SkipChecks, [switch]$SkipInstall, [switch]$UnpackedOnly)
$ErrorActionPreference = 'Stop'
$repoPath = [System.IO.Path]::GetFullPath((Join-Path $PSScriptRoot '..'))
$releasePath = Join-Path $repoPath 'release'
$issuerPath = Join-Path $repoPath 'publisher-private'
$publicPath = Join-Path $repoPath 'internal\licensecode\public.key'
$privatePath = Join-Path $issuerPath 'issuer-private.pem'
$backendPath = Join-Path $repoPath 'artifacts\desktop-backend'
$previousCgo = $env:CGO_ENABLED
$previousOS = $env:GOOS
$previousArch = $env:GOARCH
function Invoke-Checked {
    param([string]$Command, [string[]]$Arguments)
    & $Command @Arguments
    if ($LASTEXITCODE -ne 0) { throw ('命令执行失败：' + $Command + '，退出码 ' + $LASTEXITCODE) }
}
function Copy-Utf8Bom {
    param([string]$Source, [string]$Destination)
    $content = [System.IO.File]::ReadAllText($Source, [System.Text.Encoding]::UTF8)
    [System.IO.File]::WriteAllText($Destination, $content, (New-Object System.Text.UTF8Encoding($true)))
}
Push-Location $repoPath
try {
    Invoke-Checked -Command 'python' -Arguments @('-X', 'utf8', 'scripts/configure_branding.py')
    $brand = Get-Content -LiteralPath (Join-Path $repoPath 'branding.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    $desktopPackage = Get-Content -LiteralPath (Join-Path $repoPath 'desktop\package.json') -Raw -Encoding UTF8 | ConvertFrom-Json
    $env:CGO_ENABLED = '0'
    $env:GOOS = 'windows'
    $env:GOARCH = 'amd64'
    New-Item -ItemType Directory -Path $releasePath, $issuerPath, $backendPath -Force | Out-Null
    $validPublic = (Test-Path -LiteralPath $publicPath) -and ([System.IO.File]::ReadAllText($publicPath).Contains('BEGIN PUBLIC KEY'))
    if (-not $validPublic) {
        if (Test-Path -LiteralPath $privatePath) { throw '发行方私钥已存在但公钥无效，请先恢复匹配公钥；不会覆盖原私钥。' }
        Invoke-Checked -Command 'go' -Arguments @('run', './cmd/licensegen', 'keygen', '--private', $privatePath, '--public', $publicPath)
    }
    if (Test-Path -LiteralPath $privatePath) {
        Invoke-Checked -Command 'go' -Arguments @('run', './cmd/licensegen', 'check-key', '--key', $privatePath, '--public', $publicPath)
        $identityName = [System.Security.Principal.WindowsIdentity]::GetCurrent().Name
        & icacls.exe $privatePath '/inheritance:r' '/grant:r' ($identityName + ':F') 'SYSTEM:F' | Out-Null
        if ($LASTEXITCODE -ne 0) { throw '无法设置发行方私钥访问权限。' }
    }
    Push-Location (Join-Path $repoPath 'frontend')
    try {
        if (-not $SkipInstall) { Invoke-Checked -Command 'npm.cmd' -Arguments @('ci', '--no-audit', '--no-fund') }
        Invoke-Checked -Command 'npm.cmd' -Arguments @('run', 'build')
    } finally { Pop-Location }
    if (-not $SkipChecks) {
        Invoke-Checked -Command 'go' -Arguments @('test', './...', '-count=1')
        Invoke-Checked -Command 'go' -Arguments @('vet', './...')
    }
    $resourcePath = Join-Path $repoPath 'resource_windows_amd64.syso'
    $windres = Get-Command 'windres.exe' -ErrorAction SilentlyContinue
    if ($windres) {
        Push-Location (Join-Path $repoPath 'assets')
        # A PATH-resolved preprocessor avoids windres misquoting a compiler path containing spaces.
        try { Invoke-Checked -Command $windres.Source -Arguments @('--preprocessor=gcc', '--preprocessor-arg=-E', '--preprocessor-arg=-xc', '--preprocessor-arg=-DRC_INVOKED', '--input=ruilian.rc', ('--output=' + $resourcePath), '--output-format=coff', '--target=pe-x86-64') } finally { Pop-Location }
    } else { throw '缺少 windres.exe；重新打包品牌需要重新编译 Windows 图标与版本资源。' }
    $applicationPath = Join-Path $backendPath '数据服务.exe'
    Invoke-Checked -Command 'go' -Arguments @('build', '-trimpath', '-ldflags', '-s -w -H=windowsgui -buildid=', '-o', $applicationPath, '.')
    Invoke-Checked -Command 'go' -Arguments @('build', '-trimpath', '-ldflags', '-s -w -buildid=', '-o', (Join-Path $issuerPath '授权管理工具.exe'), './cmd/licensegen')
    Push-Location (Join-Path $repoPath 'desktop')
    try {
        if (-not $SkipInstall) { Invoke-Checked -Command 'npm.cmd' -Arguments @('ci', '--no-audit', '--no-fund') }
        Invoke-Checked -Command 'node' -Arguments @('node_modules/electron/install.js')
    } finally { Pop-Location }
    Invoke-Checked -Command 'python' -Arguments @('-X', 'utf8', 'scripts/collect_notices.py')
    Copy-Utf8Bom -Source (Join-Path $repoPath 'docs\使用说明.md') -Destination (Join-Path $releasePath '使用说明.txt')
    Copy-Item -LiteralPath (Join-Path $repoPath 'docs\第三方许可证.txt') -Destination (Join-Path $releasePath '第三方许可证.txt') -Force
    Copy-Item -LiteralPath (Join-Path $repoPath 'docs\第三方许可证.txt') -Destination (Join-Path $issuerPath '第三方许可证.txt') -Force
    Copy-Utf8Bom -Source (Join-Path $repoPath 'docs\字体图标与品牌配置.md') -Destination (Join-Path $releasePath '字体图标与品牌配置.txt')
    Copy-Utf8Bom -Source (Join-Path $repoPath 'docs\发行方授权工具说明.md') -Destination (Join-Path $issuerPath '发行方必读.txt')
    Push-Location (Join-Path $repoPath 'desktop')
    try {
        if ($UnpackedOnly) { Invoke-Checked -Command 'npm.cmd' -Arguments @('run', 'pack', '--', '--publish', 'never') }
        else { Invoke-Checked -Command 'npm.cmd' -Arguments @('run', 'dist', '--', '--publish', 'never') }
    } finally { Pop-Location }
    $clientPath = Join-Path $releasePath ('client\win-unpacked\' + $brand.desktop_name + '.exe')
    $installerName = $brand.installer_name + '-' + $desktopPackage.version + '-x64.exe'
    $installerPath = Join-Path $releasePath $installerName
    if (-not $UnpackedOnly) { Copy-Item -LiteralPath (Join-Path $releasePath ('client\' + $installerName)) -Destination $installerPath -Force }
    $deliverable = if ($UnpackedOnly) { $clientPath } else { $installerPath }
    $hash = (Get-FileHash -LiteralPath $deliverable -Algorithm SHA256).Hash.ToLowerInvariant()
    $buildInfo = [ordered]@{ product='RUILIAN_ERP'; display_name=$brand.app_name; version=$desktopPackage.version; platform='windows/amd64'; database='SQLite'; client='Electron 44.3.0'; font='Noto Sans SC (SIL OFL 1.1)'; icons='Element Plus Icons (MIT)'; requires_external_browser=$false; file=(Split-Path -Leaf $deliverable); sha256=$hash; bytes=(Get-Item -LiteralPath $deliverable).Length; backend_sha256=(Get-FileHash -LiteralPath $applicationPath -Algorithm SHA256).Hash.ToLowerInvariant(); built_at=[DateTime]::UtcNow.ToString('o'); includes_private_key=$false }
    [System.IO.File]::WriteAllText((Join-Path $releasePath '版本与校验.json'), ($buildInfo | ConvertTo-Json), (New-Object System.Text.UTF8Encoding($false)))
    Write-Host ('桌面客户端：' + $clientPath) -ForegroundColor Green
    if (-not $UnpackedOnly) { Write-Host ('安装程序：' + $installerPath) -ForegroundColor Green }
    Write-Host ('发行方工具：' + (Join-Path $issuerPath '授权管理工具.exe'))
    Write-Host ('交付文件 SHA-256：' + $hash)
} finally {
    Pop-Location
    $env:CGO_ENABLED = $previousCgo
    $env:GOOS = $previousOS
    $env:GOARCH = $previousArch
}
