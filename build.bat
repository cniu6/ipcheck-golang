@echo off
setlocal enabledelayedexpansion

set DIST=dist
if not exist %DIST% mkdir %DIST%
set APP=ipcheck

REM Detect ANSI color support (Windows 10+ or VSCode terminal)
set USECOLOR=0
for /f "tokens=4-5 delims=. " %%i in ('ver') do (
	set MAJOR=%%i
)
REM VSCode terminal usually supports ANSI; check TERM env
if defined TERM set USECOLOR=1
if "%MAJOR%" GEQ "10" set USECOLOR=1

if %USECOLOR%==1 (
	for /f "tokens=1,2 delims==" %%a in ('"prompt $E" ^| cmd') do set "ESC=%%b"
	set GREEN=%ESC%[92m
	set RED=%ESC%[91m
	set RESET=%ESC%[0m
) else (
	set GREEN=
	set RED=
	set RESET=
)

REM Version (optional)
for /f %%i in ('git rev-parse --short HEAD 2^>nul') do set GIT_SHA=%%i
if not defined GIT_SHA set GIT_SHA=nogit
set LDFLAGS=-s -w -X main.buildSHA=%GIT_SHA%
set CGO_ENABLED=0

REM Extended targets (common OS/ARCH combos)
set TARGETS=^
  windows/amd64 windows/arm64 windows/386 ^
  linux/amd64 linux/arm64 linux/386 linux/mips linux/mipsle linux/mips64 linux/mips64le linux/ppc64le linux/s390x ^
  darwin/amd64 darwin/arm64 ^
  freebsd/amd64 freebsd/arm64 freebsd/386 ^
  openbsd/amd64 openbsd/arm64 openbsd/386 ^
  netbsd/amd64 netbsd/arm64 netbsd/386

set FAIL=0
for %%t in (%TARGETS%) do (
	for /f "tokens=1,2 delims=/" %%a in ("%%t") do (
		set GOOS=%%a
		set GOARCH=%%b
		set EXT=
		if "!GOOS!"=="windows" set EXT=.exe
		set OUT=%DIST%\%APP%-!GOOS!-!GOARCH!!EXT!
		echo Building !OUT!
		go build -trimpath -ldflags "%LDFLAGS%" -o "!OUT!" .
		if errorlevel 1 (
			set FAIL=1
			echo %RED%[Error]%RESET% Build failed for !GOOS!/!GOARCH!
		) else (
			echo %GREEN%[Success]%RESET% !OUT!
		)
	)
)

if %FAIL%==0 (
	echo %GREEN%All builds completed successfully.%RESET%
) else (
	echo %RED%Some builds failed.%RESET%
	exit /b 1
)

endlocal 