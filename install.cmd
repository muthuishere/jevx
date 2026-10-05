@echo off
rem jevx installer (Windows): downloads the release binary, then `jevx install` puts the agent skill in
rem %USERPROFILE%\.claude\skills, %USERPROFILE%\.agents\skills (and .codex if present) and adds the Claude Code hook
rem template (disabled).
rem   curl -fsSLo install.cmd https://muthuishere.github.io/jevx/install.cmd && install.cmd
rem   set JEVX_VERSION=v0.1.0 / set JEVX_NO_HOOK=1 before running to pin a version / install skills only
setlocal
set REPO=muthuishere/jevx
if "%JEVX_BIN%"=="" set JEVX_BIN=%LOCALAPPDATA%\Programs\jevx
if "%JEVX_VERSION%"=="" set JEVX_VERSION=latest
set ARCH=amd64
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set ARCH=arm64
set ASSET=jevx_windows_%ARCH%.exe
if "%JEVX_VERSION%"=="latest" (set URL=https://github.com/%REPO%/releases/latest/download/%ASSET%) else (set URL=https://github.com/%REPO%/releases/download/%JEVX_VERSION%/%ASSET%)
if not exist "%JEVX_BIN%" mkdir "%JEVX_BIN%"
echo jevx: downloading %ASSET% (%JEVX_VERSION%)
curl -fsSL "%URL%" -o "%JEVX_BIN%\jevx.exe"
if errorlevel 1 (
  where gh >nul 2>nul || (echo jevx: download failed: %URL% & exit /b 1)
  if "%JEVX_VERSION%"=="latest" (gh release download -R %REPO% -p %ASSET% -D "%TEMP%" --clobber) else (gh release download %JEVX_VERSION% -R %REPO% -p %ASSET% -D "%TEMP%" --clobber)
  if errorlevel 1 exit /b 1
  move /Y "%TEMP%\%ASSET%" "%JEVX_BIN%\jevx.exe" >nul
)
"%JEVX_BIN%\jevx.exe" version
if "%JEVX_NO_HOOK%"=="" ("%JEVX_BIN%\jevx.exe" install) else ("%JEVX_BIN%\jevx.exe" install --skills)
echo jevx: next: set TYPESAFE_API_KEY=...   (key: console.typesafe.ai/keys; hosted Jev needs nothing else)
echo       then:  echo Prod is down ^| jevx is "Is this urgent?"     own endpoint: jevx profile add NAME URL --model M
echo %PATH% | find /I "%JEVX_BIN%" >nul || (
  powershell -NoProfile -Command "$p=[Environment]::GetEnvironmentVariable('Path','User'); [Environment]::SetEnvironmentVariable('Path', ($p.TrimEnd(';')+';%JEVX_BIN%').TrimStart(';'), 'User')"
  echo jevx: added %JEVX_BIN% to your user PATH; open a new terminal
)
endlocal
