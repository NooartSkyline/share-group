@echo off
cd /d "%~dp0"
if not exist "ShareGroup.exe" (
  echo ไม่พบ ShareGroup.exe ในโฟลเดอร์นี้
  exit /b 1
)
ShareGroup.exe -uninstall
