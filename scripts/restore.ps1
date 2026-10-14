param(
    [string]$Backup,
    [string]$DataDir
)
$ErrorActionPreference = 'Stop'
try {
    $applicationPath = Join-Path $PSScriptRoot '睿链进销存.exe'
    if (-not (Test-Path -LiteralPath $applicationPath -PathType Leaf)) { throw '请将恢复脚本放到睿链进销存.exe 同一目录。' }
    if (-not $Backup) {
        Add-Type -AssemblyName System.Windows.Forms
        $dialog = New-Object System.Windows.Forms.OpenFileDialog
        $dialog.Title = '选择睿链生成的 SQLite 备份'
        $dialog.Filter = 'SQLite 备份 (*.sqlite;*.db)|*.sqlite;*.db|所有文件 (*.*)|*.*'
        if ($dialog.ShowDialog() -ne [System.Windows.Forms.DialogResult]::OK) { exit 0 }
        $Backup = $dialog.FileName
    }
    $backupPath = (Resolve-Path -LiteralPath $Backup).Path
    if (-not (Test-Path -LiteralPath $backupPath -PathType Leaf)) { throw '备份文件不存在。' }
    if (-not $DataDir) {
        $defaultDataPath = Join-Path $PSScriptRoot 'data'
        $answer = Read-Host ('数据目录，直接回车使用 ' + $defaultDataPath)
        if ($answer) { $DataDir = $answer } else { $DataDir = $defaultDataPath }
    }
    $targetDataPath = [System.IO.Path]::GetFullPath($DataDir)
    $targetDatabasePath = Join-Path $targetDataPath 'ruilian.db'
    if ([string]::Equals($backupPath, $targetDatabasePath, [System.StringComparison]::OrdinalIgnoreCase)) { throw '备份源不能是将要替换的当前数据库。' }
    Write-Host ('备份源：' + $backupPath)
    Write-Host ('目标数据库：' + $targetDatabasePath)
    Write-Host '请先停止本机服务。恢复前将自动保存当前数据库、WAL 及安装记录的副本。'
    $confirmation = Read-Host '确认恢复请输入 RESTORE；其他输入取消'
    if ($confirmation -cne 'RESTORE') { Write-Host '已取消。'; exit 0 }
    $errorLog = Join-Path ([System.IO.Path]::GetTempPath()) ('ruilian-restore-' + [guid]::NewGuid().ToString('N') + '.log')
    $verifyArguments = @('--no-browser', '--verify-backup', ('"' + $backupPath + '"'))
    $verification = Start-Process -FilePath $applicationPath -ArgumentList $verifyArguments -Wait -PassThru -WindowStyle Hidden -RedirectStandardError $errorLog
    if ($verification.ExitCode -ne 0) { throw ('备份校验失败：' + [System.IO.File]::ReadAllText($errorLog, [System.Text.Encoding]::UTF8)) }
    $restoreArguments = @('--no-browser', '--data-dir', ('"' + $targetDataPath + '"'), '--restore', ('"' + $backupPath + '"'))
    $restoreProcess = Start-Process -FilePath $applicationPath -ArgumentList $restoreArguments -Wait -PassThru -WindowStyle Hidden -RedirectStandardError $errorLog
    if ($restoreProcess.ExitCode -ne 0) { throw ('恢复未完成：' + [System.IO.File]::ReadAllText($errorLog, [System.Text.Encoding]::UTF8)) }
    Write-Host '恢复成功。请重新启动睿链，核对业务数据与软件授权。' -ForegroundColor Green
    Write-Host ('恢复前副本保存在 ' + $targetDataPath + '\pre-restore-日期时间 目录。')
} catch {
    Write-Host $_.Exception.Message -ForegroundColor Red
    exit 1
}
