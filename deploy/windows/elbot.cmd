@echo off
rem ElBot Windows local container command entry point.
rem Equivalent to: powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0elbot.ps1" %*
powershell.exe -NoProfile -ExecutionPolicy Bypass -File "%~dp0elbot.ps1" %*
exit /b %ERRORLEVEL%
