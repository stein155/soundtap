# SoundTap installer for Windows (PowerShell 5.1 or later).
#
#   irm <site>/installer/install.ps1 | iex
#
# Finds SoundTouch speakers that still run Bose's software on this computer's networks,
# lets you pick which ones get SoundTap, and asks each to download and install it from
# SoundTap's update feed, as the browser installer does. Nothing is installed on this
# computer.
& {
$ErrorActionPreference = 'Stop'
$feed = 'https://raw.githubusercontent.com/stein155/soundtap/main/updates/soundtouch'
# A preview model installs only from the preview package (ADR 0133), which this feed names; a
# speaker is sent here only after its owner picked it and typed "preview".
$previewFeed = 'https://raw.githubusercontent.com/stein155/soundtap/main/updates/soundtouch-preview'
# This script's own site: its versions.json names the models SoundTap installs on, for this
# script, install.sh and the browser installer alike.
$site = 'https://stein155.github.io/soundtap/installer'
$waitMinutes = 10
# A SoundTouch 20 also rewrites its network chip and restarts again, and one install took
# almost nine minutes: it gets twice as long.
$waitMinutesSt20 = 20

function Test-Private([string]$a) {
    $o = $a.Split('.') | ForEach-Object { [int]$_ }
    ($o[0] -eq 10) -or ($o[0] -eq 172 -and $o[1] -ge 16 -and $o[1] -le 31) -or ($o[0] -eq 192 -and $o[1] -eq 168)
}

# Test-Rule: the speaker's /info type and module type fit one versions.json rule.
function Test-Rule($rule, [string]$type, [string]$moduleType) {
    if ($rule.type -and $type -cne $rule.type) { return $false }
    if ($rule.typeContains -and -not $type.Contains($rule.typeContains)) { return $false }
    if ($rule.moduleType -and $moduleType -cne $rule.moduleType) { return $false }
    $true
}

# Get-Verdict: Status ok, preview (installable, but nobody has run SoundTap on the model) or no
# (not supported yet), and Note, what the list says about the speaker. The Bose software version
# does not count. Unsupported rules count first, then preview rules, then supported ones: a
# SoundTouch 20 that reports the Series III's module is the Series III preview.
function Get-Verdict($table, [string]$type, [string]$moduleType) {
    foreach ($rule in @($table.unsupported)) {
        if (Test-Rule $rule $type $moduleType) {
            return [pscustomobject]@{ Status = 'no'; Note = "  ($($rule.model): not supported yet; skipped)" }
        }
    }
    $rules = @(@($table.preview) | Where-Object { $_ }) + @($table.supported)
    foreach ($rule in $rules) {
        if (-not (Test-Rule $rule $type $moduleType)) { continue }
        if ($rule.preview) {
            return [pscustomobject]@{ Status = 'preview'; Note = "  ($($rule.model): preview, worked out from Bose's software; nobody has run SoundTap on this model yet)" }
        }
        return [pscustomobject]@{ Status = 'ok'; Note = '' }
    }
    [pscustomobject]@{ Status = 'no'; Note = '  (not supported yet: SoundTap has not run on this model; skipped)' }
}

# Get-Feed: the feed to send a listed speaker to. The preview feed only for one whose verdict is
# preview, which the owner picked and confirmed; every other speaker gets the supported feed.
function Get-Feed($speaker) {
    if ($speaker.Model -eq 'preview') { $previewFeed } else { $feed }
}

# Find-Moved: the address of the speaker with that deviceID on any of the /24 networks, scanned
# as at the start, or $null. A SoundTouch 20 often comes back from the install with another
# address. SoundTap reports stock's deviceID (the board's MAC) as deviceId on /api/info.
function Find-Moved($http, [string]$id, $prefixes) {
    $scan = @{}
    foreach ($net in $prefixes) {
        foreach ($i in 1..254) { $a = "$net.$i"; $scan[$a] = $http.GetStringAsync("http://$a/api/info") }
    }
    try { [System.Threading.Tasks.Task]::WaitAll([System.Threading.Tasks.Task[]]$scan.Values) } catch { }
    foreach ($a in ($scan.Keys | Sort-Object { [version]$_ })) {
        $t = $scan[$a]
        if ($t.Status -ne 'RanToCompletion') { continue }
        if ($t.Result -match ('"deviceId": *"' + [regex]::Escape($id) + '"')) { return $a }
    }
    $null
}

# Tests load the functions above without running the installer.
if ($env:SOUNDTAP_INSTALLER_FUNCTIONS) { return }

try { $table = Invoke-RestMethod -TimeoutSec 15 -Uri "$site/versions.json" } catch { $table = $null }
if (-not ($table -and $table.supported)) {
    Write-Host "Could not load the list of supported speakers ($site/versions.json). Is this computer online?"
    return
}
Add-Type -AssemblyName System.Net.Http
$http = New-Object System.Net.Http.HttpClient
$http.Timeout = [TimeSpan]::FromSeconds(2)

Write-Host "Looking for SoundTouch speakers with Bose's software..."
# The /24 networks of this computer's private addresses; Hyper-V and WSL switches hold no speakers.
$prefixes = Get-NetIPAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue |
    Where-Object { (Test-Private $_.IPAddress) -and $_.InterfaceAlias -notmatch 'vEthernet|WSL|VirtualBox|VMware' } |
    ForEach-Object { ($_.IPAddress.Split('.')[0..2]) -join '.' } | Sort-Object -Unique
if (-not $prefixes) { Write-Host 'This computer has no private IPv4 network address.'; return }

$tasks = @{}
foreach ($net in $prefixes) {
    foreach ($i in 1..254) { $ip = "$net.$i"; $tasks[$ip] = $http.GetStringAsync("http://${ip}:8090/info") }
}
try { [System.Threading.Tasks.Task]::WaitAll([System.Threading.Tasks.Task[]]$tasks.Values) } catch { }

$found = @(); $seen = @{}
foreach ($ip in ($tasks.Keys | Sort-Object { [version]$_ })) {
    $t = $tasks[$ip]
    if ($t.Status -ne 'RanToCompletion') { continue }
    try { $info = ([xml]$t.Result).info } catch { continue }
    if (-not $info) { continue }
    if ($info.deviceID -and $seen[$info.deviceID]) { continue }  # one speaker, several addresses
    $seen[$info.deviceID] = $true
    $verdict = Get-Verdict $table $info.type $info.moduleType
    $found += [pscustomobject]@{ N = $found.Count + 1; Ip = $ip; Id = ($info.deviceID -replace '[^0-9A-Za-z]', ''); Name = $info.name; Type = $info.type; Model = $verdict.Status; Note = $verdict.Note }
}
if (-not $found) {
    Write-Host "No speaker with Bose's software answered. Is it on, and on the same network as this computer?"
    Write-Host 'Speakers that already run SoundTap are not listed: update those from their own web page.'
    return
}
foreach ($s in $found) {
    Write-Host ("  {0}) {1} - {2} at {3}{4}" -f $s.N, $s.Name, $s.Type, $s.Ip, $s.Note)
}

$ok = @($found | Where-Object { $_.Model -eq 'ok' })
$previews = @($found | Where-Object { $_.Model -eq 'preview' })
if (-not $ok -and -not $previews) { Write-Host 'None of these speakers can get SoundTap (see above).'; return }
Write-Host ''
# A preview speaker is never in "all of them": it is picked by its number, then confirmed.
$pickedPreview = $false
if ($ok.Count -eq 1 -and -not $previews) {
    $picked = @($ok[0].Ip)
    if ((Read-Host "Install SoundTap on $($ok[0].Name)? (y/n)") -notmatch '^[yYjJ]') { Write-Host 'Stopped; nothing was changed.'; return }
} else {
    $prompt = if ($ok) { 'Which speakers? Press Enter for all supported ones, or type their numbers (like: 1 3)' } else { 'Only previews here. Type the numbers of the speakers to install (like: 1 3), or press Enter to stop' }
    $choice = Read-Host $prompt
    $numbers = if ($choice.Trim()) { $choice -split '\s+' | Where-Object { $_ } } else { $ok.N }
    $picked = @()
    foreach ($k in $numbers) {
        $s = $found | Where-Object { "$($_.N)" -eq "$k" }
        if (-not $s) { Write-Host "There is no speaker $k."; return }
        if ($s.Model -eq 'preview') { $pickedPreview = $true }
        elseif ($s.Model -ne 'ok') { Write-Host "Speaker $k cannot get SoundTap (see above); skipped."; continue }
        $picked += $s.Ip
    }
    if (-not $picked) { Write-Host 'Nothing to install.'; return }
}
if ($pickedPreview) {
    Write-Host ''
    Write-Host "A preview model: SoundTap is worked out from Bose's own software and nobody has run it on"
    Write-Host 'this model yet. It may not play, its buttons or lamps may be wrong, and its way back may be'
    Write-Host 'unproven. Read first:'
    Write-Host '  https://stein155.github.io/soundtap/owners/compatibility/#previews'
    if ((Read-Host 'Type preview to install on it anyway') -cne 'preview') { Write-Host 'Stopped; nothing was changed.'; return }
}

Write-Host ''
Write-Host "SoundTap replaces Bose's software on the speaker. You can go back to Bose later:"
Write-Host '  https://stein155.github.io/soundtap/owners/install/#going-back-to-bose'
if ((Read-Host 'Continue? (y/n)') -notmatch '^[yYjJ]') { Write-Host 'Stopped; nothing was changed.'; return }

foreach ($ip in $picked) {
    $speakerFeed = Get-Feed ($found | Where-Object { $_.Ip -eq $ip } | Select-Object -First 1)
    try {
        Invoke-WebRequest -UseBasicParsing -TimeoutSec 15 -Method Post -ContentType 'text/plain' `
            -Body "<swUpdateStart indexUrl=`"$speakerFeed`"/>" -Uri "http://${ip}:8090/swUpdateStart" | Out-Null
        Write-Host "${ip}: install started."
    } catch { Write-Host "${ip}: the request was not confirmed; it may still have arrived." }
}

# Each speaker is followed by the address it had and by its deviceID, which stays the same
# when the address does not.
$ids = @{}; $st20 = $false
foreach ($ip in $picked) {
    $s = $found | Where-Object { $_.Ip -eq $ip } | Select-Object -First 1
    $ids[$ip] = $s.Id
    if ($s.Type -ceq 'SoundTouch 20') { $st20 = $true }
}
if ($st20) { $waitMinutes = $waitMinutesSt20 }

Write-Host ''
Write-Host 'Installing. Leave the speakers alone and keep them plugged in; this takes about five minutes.'
if ($st20) { Write-Host "A SoundTouch 20 takes longer, and often comes back at another address: this waits up to $waitMinutes minutes." }
$start = Get-Date
$end = $start.AddMinutes($waitMinutes)
$shown = 0
$scanned = $start
$left = $picked
while ($left -and (Get-Date) -lt $end) {
    Start-Sleep -Seconds 10
    # Once a minute after the first two, look on the whole network for speakers that are not
    # back at their old address.
    $rescan = ((Get-Date) - $start).TotalSeconds -ge 120 -and ((Get-Date) - $scanned).TotalSeconds -ge 60
    if ($rescan) { $scanned = Get-Date }
    $left = @($left | Where-Object {
        $ip = $_
        $at = $null
        try {
            $v = Invoke-RestMethod -TimeoutSec 4 -Uri "http://$ip/api/version"
            if ($null -ne $v.api) { $at = $ip }
        } catch { }
        if (-not $at -and $rescan -and $ids[$ip]) { $at = Find-Moved $http $ids[$ip] $prefixes }
        if ($at) {
            if ($at -ne $ip) { Write-Host "${ip}: the speaker came back at a new address, $at." }
            Write-Host "${at}: done, it runs SoundTap. Opening http://$at/ to set it up."
            try { Start-Process "http://$at/" } catch { }
            return $false
        }
        $true
    })
    $m = [int][math]::Floor(((Get-Date) - $start).TotalMinutes)
    if ($left -and $m -gt $shown) { $shown = $m; Write-Host "  still installing... $m min" }
}
foreach ($ip in $left) {
    Write-Host "${ip}: SoundTap not confirmed after $waitMinutes minutes. Keep it powered and look for it later:"
    Write-Host "    at http://$ip/, under its name in your router's list of devices (its address may have"
    Write-Host "    changed), or at http://soundtap.local. Or see 'When something goes wrong' in the install guide."
}
}
