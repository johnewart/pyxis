$ErrorActionPreference = "Stop"

$here = $PSScriptRoot
Set-Location $here

$env:CGO_ENABLED = "1"

function Test-SqliteHeader([string] $dir) {
  return Test-Path (Join-Path $dir "sqlite3.h")
}

# Prefer MSYS2 MinGW-w64 headers/libs (GitHub-hosted runners ship MSYS2 under C:\msys64).
$includeDirs = @(
  "C:/msys64/mingw64/include",
  "C:/tools/msys64/mingw64/include"
)

$include = $null
foreach ($d in $includeDirs) {
  if (Test-SqliteHeader $d) {
    $include = $d
    break
  }
}

if ($null -eq $include) {
  throw @"
SQLite development headers (sqlite3.h) not found.

On Windows CI or dev machines, install the MinGW-w64 SQLite package, for example:

  C:\msys64\usr\bin\bash.exe -lc "pacman -S --needed --noconfirm mingw-w64-x86_64-sqlite"

Then ensure C:\msys64\mingw64\bin is on PATH before running this script (the release workflow adds it).
"@
}

$mingwRoot = Split-Path $include
$libDir = Join-Path $mingwRoot "lib"
$incUnix = ($include -replace "\\", "/")
$libUnix = ($libDir -replace "\\", "/")

$env:CGO_CFLAGS = "-I$incUnix"

if (
  (Test-Path (Join-Path $libDir "libsqlite3.dll.a")) -or
  (Test-Path (Join-Path $libDir "libsqlite3.a"))
) {
  $env:CGO_LDFLAGS = "-L$libUnix -lsqlite3"
}

# Prefer explicit MinGW GCC so we do not accidentally pick MSVC `cl`.
if (Get-Command x86_64-w64-mingw32-gcc -ErrorAction SilentlyContinue) {
  $env:CC = "x86_64-w64-mingw32-gcc"
}
elseif (Get-Command gcc -ErrorAction SilentlyContinue) {
  $env:CC = "gcc"
}
else {
  throw "No MinGW gcc found on PATH (expected x86_64-w64-mingw32-gcc or gcc from MSYS2 mingw64/bin)."
}

function Resolve-Protoc {
  if ($env:PROTOC) {
    return $env:PROTOC
  }

  $bundledRoot = Join-Path $here "packages\client\node_modules\@protobuf-ts\protoc\installed"
  if (Test-Path $bundledRoot) {
    $bundled = Get-ChildItem -Path $bundledRoot -Include "protoc.exe", "protoc" -Recurse -File -ErrorAction SilentlyContinue |
      Select-Object -First 1
    if ($bundled) {
      return $bundled.FullName
    }
  }

  if (Get-Command protoc -ErrorAction SilentlyContinue) {
    return "protoc"
  }

  throw @"
protoc not found. Proto generation requires one of:
  - npm install in llmd/packages/client (bundled @protobuf-ts/protoc)
  - PROTOC env var pointing to a protoc binary
  - protoc on PATH
"@
}

function Invoke-GenerateProtos {
  Write-Host "Running: proto generate (native PowerShell)"

  $goBin = (go env GOBIN).Trim()
  if (-not $goBin) {
    $goBin = Join-Path (go env GOPATH) "bin"
  }
  $env:PATH = "$goBin;$env:PATH"

  go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11
  if ($LASTEXITCODE -ne 0) {
    throw "protoc-gen-go install failed with exit code $LASTEXITCODE"
  }

  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.5.1
  if ($LASTEXITCODE -ne 0) {
    throw "protoc-gen-go-grpc install failed with exit code $LASTEXITCODE"
  }

  $staleProto = Join-Path $here "gen\llmv1\embedding.pb.go"
  if (Test-Path $staleProto) {
    Remove-Item -Force $staleProto
  }

  $protoc = Resolve-Protoc
  $protoDir = Join-Path $here "proto"
  # Keep in lockstep with LLMD_PROTOS in the Makefile (and scripts/protogen.mjs):
  # only the protos llmd owns. Other services own their own schemas.
  $protos = @(
    "knowledge/v1/knowledge.proto",
    "llamacpp/v1/llamacpp.proto",
    "llmv1/llm.proto",
    "mcpproxy/v1/mcpproxy.proto"
  )

  & $protoc -I $protoDir `
    --go_out=gen --go_opt=paths=source_relative `
    --go-grpc_out=gen --go-grpc_opt=paths=source_relative `
    @protos

  if ($LASTEXITCODE -ne 0) {
    throw "protoc generate failed with exit code $LASTEXITCODE"
  }
}

# Generate protobuf Go code before building (same as Makefile generate; no MSYS2 make required)
$mfile = Join-Path $here "Makefile"
if (Test-Path $mfile) {
  Invoke-GenerateProtos
}

# cmd/llmd is its own module, so it must be built with -C; from the repo root it
# resolves to no packages at all.
go build -C (Join-Path $here "cmd/llmd") -trimpath -ldflags="-s -w" -o (Join-Path $here "llmd-windows-amd64.exe") ./...
