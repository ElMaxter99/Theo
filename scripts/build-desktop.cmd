@echo off
rem Builds dist\theo-desktop.exe without caring about PowerShell's execution policy.
rem   scripts\build-desktop.cmd                  build
rem   scripts\build-desktop.cmd -InstallDeps -Run  install Go/gcc if missing, build and open the app
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0build-desktop.ps1" %*
exit /b %ERRORLEVEL%
