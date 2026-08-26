#!/usr/bin/env pwsh
#requires -Version 5.1
<#
.SYNOPSIS
    Builds and validates all packages before tagging and publishing a GitHub release.
.EXAMPLE
    ./release.ps1
    ./release.ps1 v1.0.0
#>
param(
    [Parameter(Position = 0)]
    [string]$Version = ""
)

Set-StrictMode -Version Latest
$ErrorActionPreference = "Stop"

function Invoke-Git {
    param(
        [Parameter(Mandatory = $true)]
        [string[]]$Arguments,
        [switch]$DiscardOutput
    )

    if ($DiscardOutput) {
        & git @Arguments 2>&1 | Out-Null
    }
    else {
        & git @Arguments
    }
    if ($LASTEXITCODE -ne 0) {
        throw "git $($Arguments -join ' ') failed with exit code $LASTEXITCODE"
    }
}

function Test-RemoteTag {
    param([Parameter(Mandatory = $true)][string]$Tag)

    & git ls-remote --exit-code --tags origin "refs/tags/$Tag" 2>&1 | Out-Null
    $exitCode = $LASTEXITCODE
    if ($exitCode -eq 0) {
        return $true
    }
    if ($exitCode -eq 2) {
        return $false
    }
    throw "Could not check remote tag '$Tag'. git ls-remote exited with code $exitCode."
}

function Get-GitHubReleaseByTag {
    param(
        [Parameter(Mandatory = $true)][string]$Owner,
        [Parameter(Mandatory = $true)][string]$Repository,
        [Parameter(Mandatory = $true)][string]$Tag,
        [Parameter(Mandatory = $true)][hashtable]$Headers
    )

    $encodedTag = [Uri]::EscapeDataString($Tag)
    $uri = "https://api.github.com/repos/$Owner/$Repository/releases/tags/$encodedTag"
    try {
        return Invoke-RestMethod -Uri $uri -Method Get -Headers $Headers
    }
    catch {
        $response = $_.Exception.Response
        if ($response -and ([int]$response.StatusCode -eq 404)) {
            return $null
        }
        throw
    }
}

function Assert-ReleaseAssets {
    param(
        [Parameter(Mandatory = $true)][string]$DistDirectory,
        [Parameter(Mandatory = $true)][string]$ReleaseVersion
    )

    $expected = @(
        @{ Name = "ollamabot-$ReleaseVersion-windows-amd64.zip"; Binary = "ollamabot.exe"; Format = "zip" },
        @{ Name = "ollamabot-$ReleaseVersion-windows-arm64.zip"; Binary = "ollamabot.exe"; Format = "zip" },
        @{ Name = "ollamabot-$ReleaseVersion-linux-amd64.tar.gz"; Binary = "ollamabot"; Format = "targz" },
        @{ Name = "ollamabot-$ReleaseVersion-linux-arm64.tar.gz"; Binary = "ollamabot"; Format = "targz" },
        @{ Name = "ollamabot-$ReleaseVersion-darwin-amd64-intel.tar.gz"; Binary = "ollamabot"; Format = "targz" },
        @{ Name = "ollamabot-$ReleaseVersion-darwin-arm64-applesilicon.tar.gz"; Binary = "ollamabot"; Format = "targz" }
    )

    Add-Type -AssemblyName System.IO.Compression.FileSystem
    $validated = @()
    foreach ($asset in $expected) {
        $path = Join-Path $DistDirectory $asset.Name
        if (-not (Test-Path -LiteralPath $path -PathType Leaf)) {
            throw "Expected build artifact is missing: $($asset.Name)"
        }
        $file = Get-Item -LiteralPath $path
        if ($file.Length -le 0) {
            throw "Build artifact is empty: $($asset.Name)"
        }

        if ($asset.Format -eq "zip") {
            $archive = [System.IO.Compression.ZipFile]::OpenRead($path)
            try {
                $entryNames = @($archive.Entries | ForEach-Object { $_.FullName.TrimEnd('/') })
                if (($entryNames -notcontains $asset.Binary) -or ($entryNames -notcontains ".env.example")) {
                    throw "Archive '$($asset.Name)' does not contain $($asset.Binary) and .env.example at its root."
                }
            }
            finally {
                $archive.Dispose()
            }
        }
        else {
            if (-not (Get-Command tar -ErrorAction SilentlyContinue)) {
                throw "tar is required to validate $($asset.Name)."
            }
            $entryNames = @(& tar -tzf $path)
            if ($LASTEXITCODE -ne 0) {
                throw "Archive validation failed for $($asset.Name)."
            }
            $normalizedEntries = @($entryNames | ForEach-Object { $_.TrimStart([char[]]"./") })
            if (($normalizedEntries -notcontains $asset.Binary) -or ($normalizedEntries -notcontains ".env.example")) {
                throw "Archive '$($asset.Name)' does not contain $($asset.Binary) and .env.example at its root."
            }
        }

        $validated += $file
        Write-Host "[OK] $($asset.Name) ($($file.Length) bytes)" -ForegroundColor Green
    }

    $actualFiles = @(Get-ChildItem -LiteralPath $DistDirectory -File)
    if ($actualFiles.Count -ne $expected.Count) {
        $actualNames = $actualFiles.Name -join ", "
        throw "dist contains unexpected or missing files. Expected $($expected.Count), found $($actualFiles.Count): $actualNames"
    }

    return $validated
}

if (-not (Test-Path -LiteralPath (Join-Path $PSScriptRoot ".git") -PathType Container)) {
    Write-Host "[ERROR] Run this script from the repository root." -ForegroundColor Red
    exit 1
}

Push-Location $PSScriptRoot
try {
    if (-not (Get-Command git -ErrorAction SilentlyContinue)) {
        throw "git is not installed or is not available in PATH."
    }

    Write-Host "Fetching tags from origin..." -ForegroundColor DarkGray
    Invoke-Git -Arguments @("fetch", "origin", "--tags", "--prune", "--force") -DiscardOutput

    $latestTag = (& git tag --sort=-version:refname | Select-Object -First 1)
    if ($latestTag) {
        $latestTag = $latestTag.Trim()
        Write-Host "Latest tag: $latestTag" -ForegroundColor Cyan
    }
    else {
        Write-Host "Latest tag: none" -ForegroundColor Yellow
    }

    if ([string]::IsNullOrWhiteSpace($Version)) {
        $Version = (Read-Host "Version to release (format vX.Y.Z)").Trim()
    }
    if ($Version -notmatch '^v\d+\.\d+\.\d+(-[a-zA-Z0-9.]+)?$') {
        throw "Version must use the format vX.Y.Z (for example, v1.0.0)."
    }

    $status = @(git status --porcelain)
    if ($LASTEXITCODE -ne 0) {
        throw "Could not read git status."
    }
    if ($status.Count -gt 0) {
        Write-Host "[ERROR] The repository has uncommitted changes:" -ForegroundColor Red
        $status | ForEach-Object { Write-Host $_ -ForegroundColor Yellow }
        throw "Commit or stash local changes before releasing."
    }

    $branch = (& git branch --show-current).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($branch)) {
        throw "Could not determine the current branch. Detached HEAD is not supported."
    }

    $remoteUrl = (& git remote get-url origin).Trim()
    if ($LASTEXITCODE -ne 0) {
        throw "Could not read the origin remote URL."
    }
    if ($remoteUrl -match 'github\.com[:/]([^/]+)/([^/.]+?)(\.git)?$') {
        $owner = $Matches[1]
        $repo = $Matches[2]
    }
    else {
        throw "Could not determine the GitHub owner and repository from origin: $remoteUrl"
    }

    $token = $env:GITHUB_TOKEN
    if ([string]::IsNullOrWhiteSpace($token)) {
        Write-Host "GITHUB_TOKEN was not found." -ForegroundColor Yellow
        $secureToken = Read-Host "GitHub token with repository release permissions" -AsSecureString
        if (-not $secureToken) {
            throw "A GitHub token is required."
        }
        $bstr = [System.Runtime.InteropServices.Marshal]::SecureStringToBSTR($secureToken)
        try {
            $token = [System.Runtime.InteropServices.Marshal]::PtrToStringAuto($bstr)
        }
        finally {
            [System.Runtime.InteropServices.Marshal]::ZeroFreeBSTR($bstr)
        }
    }

    $headers = @{
        "Authorization"        = "Bearer $token"
        "Accept"               = "application/vnd.github+json"
        "X-GitHub-Api-Version" = "2022-11-28"
    }

    $localTagExists = [bool](& git tag -l $Version)
    $remoteTagExists = Test-RemoteTag -Tag $Version
    $existingRelease = Get-GitHubReleaseByTag -Owner $owner -Repository $repo -Tag $Version -Headers $headers
    $replacing = $localTagExists -or $remoteTagExists -or ($null -ne $existingRelease)

    Write-Host ""
    Write-Host "=============================================" -ForegroundColor Cyan
    Write-Host "              RELEASE SUMMARY                " -ForegroundColor Cyan
    Write-Host "=============================================" -ForegroundColor Cyan
    Write-Host "Latest tag:     $(if ($latestTag) { $latestTag } else { 'none' })" -ForegroundColor Gray
    Write-Host "New version:    $Version" -ForegroundColor Gray
    Write-Host "Repository:     $owner/$repo" -ForegroundColor Gray
    Write-Host "Source branch:  $branch" -ForegroundColor Gray
    Write-Host "Replace mode:   $replacing" -ForegroundColor $(if ($replacing) { "Yellow" } else { "Gray" })
    Write-Host "=============================================" -ForegroundColor Cyan
    if ($replacing) {
        Write-Host "An existing tag or release will be deleted only after all builds pass validation." -ForegroundColor Yellow
    }

    $confirmation = (Read-Host "Continue? (y/n)").Trim().ToLowerInvariant()
    if ($confirmation -notin @("y", "yes")) {
        Write-Host "Release cancelled." -ForegroundColor Yellow
        exit 0
    }

    Write-Host "`n[1/5] Building all release packages..." -ForegroundColor Cyan
    $buildScript = Join-Path $PSScriptRoot "build-all.ps1"
    if (-not (Test-Path -LiteralPath $buildScript -PathType Leaf)) {
        throw "Build script was not found: $buildScript"
    }
    & $buildScript -Version $Version
    if ($LASTEXITCODE -ne 0) {
        throw "The multiplatform build failed. No tag or release was modified."
    }

    Write-Host "`n[2/5] Validating release packages..." -ForegroundColor Cyan
    $distDir = Join-Path $PSScriptRoot "dist"
    $assets = @(Assert-ReleaseAssets -DistDirectory $distDir -ReleaseVersion $Version)
    Write-Host "[OK] All $($assets.Count) release packages passed validation." -ForegroundColor Green

    Write-Host "`n[3/5] Pushing source branch..." -ForegroundColor Cyan
    Invoke-Git -Arguments @("push", "origin", $branch)

    if ($replacing) {
        Write-Host "`nReplacing existing release/tag '$Version'..." -ForegroundColor Yellow
        if ($null -ne $existingRelease) {
            $deleteReleaseUrl = "https://api.github.com/repos/$owner/$repo/releases/$($existingRelease.id)"
            Invoke-RestMethod -Uri $deleteReleaseUrl -Method Delete -Headers $headers | Out-Null
            Write-Host "[OK] Existing GitHub release deleted." -ForegroundColor Green
        }
        if ($remoteTagExists) {
            Invoke-Git -Arguments @("push", "origin", "--delete", "refs/tags/$Version")
            Write-Host "[OK] Existing remote tag deleted." -ForegroundColor Green
        }
        if ($localTagExists) {
            Invoke-Git -Arguments @("tag", "-d", $Version) -DiscardOutput
            Write-Host "[OK] Existing local tag deleted." -ForegroundColor Green
        }
    }

    Write-Host "`n[4/5] Creating and pushing tag '$Version'..." -ForegroundColor Cyan
    Invoke-Git -Arguments @("tag", $Version)
    try {
        Invoke-Git -Arguments @("push", "origin", "refs/tags/$Version")
    }
    catch {
        Invoke-Git -Arguments @("tag", "-d", $Version) -DiscardOutput
        throw
    }

    Write-Host "`n[5/5] Creating GitHub release and uploading validated assets..." -ForegroundColor Cyan
    $releaseUrl = "https://api.github.com/repos/$owner/$repo/releases"
    $releaseBody = @{
        tag_name               = $Version
        target_commitish       = $branch
        name                   = "Release $Version"
        body                   = "Release $Version generated by release.ps1 after local build validation."
        draft                  = $false
        prerelease             = $false
        generate_release_notes = $true
    } | ConvertTo-Json -Depth 10

    $response = Invoke-RestMethod -Uri $releaseUrl -Method Post -Headers $headers -Body $releaseBody -ContentType "application/json; charset=utf-8"
    $uploadUrlBase = $response.upload_url -replace '\{.*?\}', ''
    foreach ($asset in $assets) {
        $fileName = $asset.Name
        $encodedName = [Uri]::EscapeDataString($fileName)
        $uploadUrl = "${uploadUrlBase}?name=$encodedName"
        $uploadHeaders = @{
            "Authorization"        = "Bearer $token"
            "Accept"               = "application/vnd.github+json"
            "X-GitHub-Api-Version" = "2022-11-28"
            "Content-Type"         = "application/octet-stream"
        }
        Write-Host "Uploading $fileName..." -ForegroundColor Cyan
        Invoke-RestMethod -Uri $uploadUrl -Method Post -Headers $uploadHeaders -InFile $asset.FullName | Out-Null
        Write-Host "[OK] Uploaded $fileName" -ForegroundColor Green
    }

    Write-Host "`n[OK] Release completed: $($response.html_url)" -ForegroundColor Green
}
catch {
    Write-Host "`n[ERROR] $($_.Exception.Message)" -ForegroundColor Red
    exit 1
}
finally {
    Pop-Location
}
